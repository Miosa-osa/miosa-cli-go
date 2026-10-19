package commands_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const bucketID = "c1c1c1c1-0000-4000-8000-000000000001"

// storageServer is a tiny in-memory bucket API for the object commands.
type storageServer struct {
	mu       sync.Mutex
	objects  map[string][]byte
	types    map[string]string
	parts    map[int][]byte
	mpKey    string
	aborted  bool
	requests []string
}

func newStorageServer(t *testing.T) *storageServer {
	s := &storageServer{objects: map[string][]byte{}, types: map[string]string{}, parts: map[int][]byte{}}
	srv := plainServer(t, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		base := "/storage/buckets/" + bucketID
		p := strings.TrimPrefix(r.URL.Path, base)
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "PUT" && strings.HasPrefix(p, "/objects/"):
			key := strings.TrimPrefix(p, "/objects/")
			s.objects[key] = body
			s.types[key] = r.Header.Get("Content-Type")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == "GET" && strings.HasPrefix(p, "/objects/"):
			data, ok := s.objects[strings.TrimPrefix(p, "/objects/")]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND"}}`))
				return
			}
			_, _ = w.Write(data)
		case r.Method == "DELETE" && strings.HasPrefix(p, "/objects/"):
			delete(s.objects, strings.TrimPrefix(p, "/objects/"))
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == "POST" && p == "/multipart":
			var in map[string]string
			_ = json.Unmarshal(body, &in)
			s.mpKey = in["key"]
			_, _ = w.Write([]byte(`{"data":{"upload_id":"up1"}}`))
		case r.Method == "PUT" && strings.Contains(p, "/multipart/up1/parts/"):
			num := p[strings.LastIndex(p, "/")+1:]
			var n int
			_, _ = fmt.Sscan(num, &n)
			s.parts[n] = body
			_, _ = w.Write([]byte(`{"data":{"etag":"e` + num + `"}}`))
		case r.Method == "POST" && p == "/multipart/up1/complete":
			var in struct {
				Parts []struct {
					N int `json:"part_number"`
				} `json:"parts"`
			}
			_ = json.Unmarshal(body, &in)
			var all []byte
			for _, part := range in.Parts {
				all = append(all, s.parts[part.N]...)
			}
			s.objects[s.mpKey] = all
			_, _ = w.Write([]byte(`{"ok":true}`))
		case r.Method == "DELETE" && p == "/multipart/up1":
			s.aborted = true
			w.WriteHeader(204)
		case r.Method == "POST" && p == "/presign":
			var in map[string]any
			_ = json.Unmarshal(body, &in)
			u := fmt.Sprintf("https://api.test/gateway/x?method=%v&ttl=%v", in["method"], in["expires_in"])
			_, _ = w.Write([]byte(`{"url":"` + u + `"}`))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"no route in test: ` + r.Method + " " + r.URL.Path + `"}}`))
		}
	})
	setupEnv(t, srv)
	return s
}

func TestStoragePutSendsTheFileAndGetReturnsTheSameBytes(t *testing.T) {
	s := newStorageServer(t)
	dir := t.TempDir()
	data := make([]byte, 300_000)
	_, _ = rand.Read(data)
	src := filepath.Join(dir, "App.dmg")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if out, err := run(t, "storage", "put", bucketID, src, "builds/App.dmg"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !bytes.Equal(s.objects["builds/App.dmg"], data) {
		t.Fatal("uploaded bytes differ")
	}

	dest := filepath.Join(dir, "down.dmg")
	if out, err := run(t, "storage", "get", bucketID, "builds/App.dmg", dest); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("downloaded bytes differ")
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Fatal("the partial file must be gone")
	}

	if _, err := run(t, "storage", "get", bucketID, "nope", filepath.Join(dir, "nope")); err == nil {
		t.Fatal("a missing object must fail")
	}
	if _, err := os.Stat(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("no file may be created for a failed download")
	}
}

func TestStorageKeyDefaultsToTheFileNameAndGetWritesToStdout(t *testing.T) {
	s := newStorageServer(t)
	src := filepath.Join(t.TempDir(), "note.txt")
	_ = os.WriteFile(src, []byte("hello"), 0o600)
	if _, err := run(t, "storage", "put", bucketID, src); err != nil {
		t.Fatal(err)
	}
	if string(s.objects["note.txt"]) != "hello" {
		t.Fatalf("key should default to note.txt: %v", s.requests)
	}
	if s.types["note.txt"] == "" {
		t.Fatal("a content type must be sent")
	}
	out, err := run(t, "storage", "get", bucketID, "note.txt", "-")
	if err != nil || out != "hello" {
		t.Fatalf("stdout download: %q %v", out, err)
	}
}

func TestStorageRmObjectAndPresign(t *testing.T) {
	s := newStorageServer(t)
	s.objects["a/b.txt"] = []byte("x")

	out, err := run(t, "storage", "presign", bucketID, "a/b.txt", "--ttl", "10m")
	if err != nil || !strings.Contains(out, "ttl=600") || !strings.Contains(out, "method=GET") {
		t.Fatalf("presign: %q %v", out, err)
	}
	out, err = run(t, "storage", "presign", bucketID, "in.bin", "--method", "put", "--ttl", "90")
	if err != nil || !strings.Contains(out, "method=PUT") || !strings.Contains(out, "ttl=90") {
		t.Fatalf("presign PUT: %q %v", out, err)
	}
	if _, err := run(t, "storage", "presign", bucketID, "k", "--ttl", "soon"); err == nil {
		t.Fatal("a bad ttl must be refused")
	}
	if _, err := run(t, "storage", "presign", bucketID, "k", "--method", "DELETE"); err == nil {
		t.Fatal("only GET and PUT are allowed")
	}

	if _, err := run(t, "storage", "rm-object", bucketID, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.objects["a/b.txt"]; ok {
		t.Fatal("the object must be deleted")
	}
}

func TestStorageObjectsShowsRealRowsFromBothServerShapes(t *testing.T) {
	route := "GET /storage/buckets/" + bucketID + "/objects"

	fakeRoutes(t, map[string]any{route: `{"data":{"keys":["a.txt"],"objects":[{"key":"a.txt","size":2048,"last_modified":"2026-10-19T00:00:00Z"}],"truncated":false}}`})
	out, err := run(t, "storage", "objects", bucketID)
	if err != nil || !strings.Contains(out, "a.txt") || !strings.Contains(out, "2") || strings.Contains(out, "No results") {
		t.Fatalf("new shape: %q %v", out, err)
	}

	fakeRoutes(t, map[string]any{route: `{"data":{"keys":["old/one.txt","old/two.txt"],"truncated":false}}`})
	out, err = run(t, "storage", "objects", bucketID)
	if err != nil || !strings.Contains(out, "old/one.txt") || !strings.Contains(out, "old/two.txt") {
		t.Fatalf("keys-only shape: %q %v", out, err)
	}

	f := fakeRoutes(t, map[string]any{route: `{"data":{"keys":[],"objects":[]}}`})
	if _, err := run(t, "storage", "objects", bucketID, "--prefix", "p/", "-n", "5"); err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery(f.find("GET", "/storage/buckets/"+bucketID+"/objects")[0].Query)
	if q.Get("prefix") != "p/" || q.Get("max_keys") != "5" {
		t.Fatalf("query = %v", q)
	}
}

func TestStorageKeysCreateListRevoke(t *testing.T) {
	creds := "/storage/buckets/" + bucketID + "/credentials"
	f := fakeRoutes(t, map[string]any{
		"POST " + creds:           `{"data":{"id":"k1","access_key_id":"MSAKX","secret":"mss_abc","permission":"read_write"},"meta":{"secret_shown_once":true}}`,
		"GET " + creds:            `{"data":[{"id":"k1","access_key_id":"MSAKX","name":"ci","permission":"read","status":"active"}]}`,
		"DELETE " + creds + "/k1": `{"data":{"id":"k1"}}`,
	})
	out, err := run(t, "storage", "keys", "create", bucketID, "--read-write", "--prefix", "app/", "--name", "ci")
	if err != nil || !strings.Contains(out, "mss_abc") {
		t.Fatalf("create: %q %v", out, err)
	}
	body := f.find("POST", creds)[0].Body
	if body["permission"] != "read_write" || body["key_prefix"] != "app/" || body["name"] != "ci" {
		t.Fatalf("body = %v", body)
	}
	if _, ok := body["read_write"]; ok {
		t.Fatal("the read_write flag must not be sent as a field")
	}

	if _, err := run(t, "storage", "keys", "create", bucketID); err != nil {
		t.Fatal(err)
	}
	if last := f.find("POST", creds)[1].Body; last["permission"] != "read" {
		t.Fatalf("default must be read only: %v", last)
	}

	out, err = run(t, "storage", "keys", "list", bucketID)
	if err != nil || !strings.Contains(out, "MSAKX") || strings.Contains(out, "mss_") {
		t.Fatalf("list: %q %v", out, err)
	}
	if _, err := run(t, "storage", "keys", "revoke", bucketID, "k1", "-y"); err != nil {
		t.Fatal(err)
	}
	if len(f.find("DELETE", creds+"/k1")) != 1 {
		t.Fatal("revoke must delete the credential")
	}
}

func TestStoragePutOverTheSingleLimitUsesParts(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 270 MiB file")
	}
	s := newStorageServer(t)
	src := filepath.Join(t.TempDir(), "big.bin")
	f, _ := os.Create(src)
	chunk := make([]byte, 1<<20)
	_, _ = rand.Read(chunk)
	for i := 0; i < 270; i++ {
		_, _ = f.Write(chunk)
	}
	_ = f.Close()

	if out, err := run(t, "storage", "put", bucketID, src, "big/one.bin"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := s.objects["big/one.bin"]; len(got) != 270<<20 || !bytes.Equal(got[:1<<20], chunk) {
		t.Fatalf("assembled object is %d bytes", len(got))
	}
	if len(s.parts) != 5 {
		t.Fatalf("270 MiB in 64 MiB parts is 5 parts, got %d", len(s.parts))
	}
}
