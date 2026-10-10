package envspec

import (
	"strings"
	"testing"
)

func TestValidateSetupScript(t *testing.T) {
	tests := []struct {
		name    string
		in      []byte
		wantErr string
	}{
		{"ok", []byte("#!/bin/bash\necho hi\n"), ""},
		{"exactly 64KB", []byte(strings.Repeat("a", MaxSetupScriptBytes)), ""},
		{"64KB plus one", []byte(strings.Repeat("a", MaxSetupScriptBytes+1)), "64KB"},
		{"empty", nil, "empty"},
		{"invalid utf8", []byte{0xff, 0xfe, 'a'}, "UTF-8"},
		{"nul byte", []byte("echo\x00hi"), "NUL"},
		{"multibyte ok", []byte("echo \"héllo ✓\""), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSetupScript(tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateVarName(t *testing.T) {
	good := []string{"A", "_x", "STRIPE_KEY", "a1_b2", strings.Repeat("A", MaxVarNameLen)}
	bad := []string{"", "1A", "A-B", "A B", "A.B", strings.Repeat("A", MaxVarNameLen+1), "é"}
	for _, n := range good {
		if err := ValidateVarName(n); err != nil {
			t.Errorf("%q should be valid: %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateVarName(n); err == nil {
			t.Errorf("%q should be invalid", n)
		}
	}
}

func TestParseAssignment(t *testing.T) {
	k, v, err := ParseAssignment("URL=postgres://u:p@h/db?a=b")
	if err != nil || k != "URL" || v != "postgres://u:p@h/db?a=b" {
		t.Fatalf("got %q %q %v", k, v, err)
	}
	if _, v, err := ParseAssignment("EMPTY="); err != nil || v != "" {
		t.Fatalf("empty value should be allowed: %q %v", v, err)
	}
	_, _, err = ParseAssignment("noequals-supersecretvalue")
	if err == nil {
		t.Fatal("want error")
	}
	_, _, err = ParseAssignment("1BAD=secretvalue123")
	if err == nil || strings.Contains(err.Error(), "secretvalue123") {
		t.Fatalf("error must not echo the value: %v", err)
	}
}

func TestParseDotenv(t *testing.T) {
	src := "\ufeff# comment\n\nA=1\nexport B=\"two words\"\nC='x'\nA=3\nD=a=b\nE=\n  F = spaced  \n"
	got, err := ParseDotenv(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []Pair{{"A", "3"}, {"B", "two words"}, {"C", "x"}, {"D", "a=b"}, {"E", ""}, {"F", "spaced"}}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("pair %d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestParseDotenvErrorsNameLineNotContent(t *testing.T) {
	_, err := ParseDotenv(strings.NewReader("A=1\nthis-is-a-secret-without-equals\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("want line-2 error without content, got %v", err)
	}
	_, err = ParseDotenv(strings.NewReader("9X=1\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("want line-1 error, got %v", err)
	}
}

func TestValidateSecretPath(t *testing.T) {
	good := []string{".env", "backend/.env", "repo/a/b.json", ".config/svc.json"}
	bad := []string{"", "/etc/passwd", "../x", "a/../b", "a//b", "a/", "a\\b", "a\x00b", "a\nb"}
	for _, p := range good {
		if err := ValidateSecretPath(p); err != nil {
			t.Errorf("%q should be valid: %v", p, err)
		}
	}
	for _, p := range bad {
		if err := ValidateSecretPath(p); err == nil {
			t.Errorf("%q should be invalid", p)
		}
	}
}

func TestValidateRepo(t *testing.T) {
	for _, r := range []string{"octocat/hello-world", "a/b"} {
		if err := ValidateRepo(r); err != nil {
			t.Errorf("%q: %v", r, err)
		}
	}
	for _, r := range []string{"", "hello", "a/b/c", "/b", "a/", "a b/c"} {
		if err := ValidateRepo(r); err == nil {
			t.Errorf("%q should be invalid", r)
		}
	}
}

func TestParseOnOff(t *testing.T) {
	for in, want := range map[string]bool{"on": true, "ON": true, "true": true, "off": false, "Off": false, "false": false} {
		got, err := ParseOnOff(in)
		if err != nil || got != want {
			t.Errorf("%q: got %v %v", in, got, err)
		}
	}
	if _, err := ParseOnOff("maybe"); err == nil {
		t.Error("want error for maybe")
	}
}

func TestMaskNeverLeaks(t *testing.T) {
	if got := Mask("sk_live_123456"); strings.Contains(got, "sk") || strings.Contains(got, "123") {
		t.Errorf("mask leaked: %q", got)
	}
	if Mask("a") != Mask("a-much-longer-secret-value") {
		t.Error("mask must not reveal length")
	}
}

func TestReadSetupFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := dir + "/" + name
		if err := writeFile(p, b); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if s, err := ReadSetupFile(write("ok.sh", []byte("echo hi\n"))); err != nil || s != "echo hi\n" {
		t.Fatalf("ok: %q %v", s, err)
	}
	if _, err := ReadSetupFile(write("big.sh", []byte(strings.Repeat("a", MaxSetupScriptBytes+1)))); err == nil {
		t.Fatal("want size error")
	}
	if _, err := ReadSetupFile(dir + "/missing.sh"); err == nil {
		t.Fatal("want missing-file error")
	}
}
