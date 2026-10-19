package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// notionImportOp turns a Notion export zip that already sits in a bucket into a
// clean Markdown tree in the same bucket. The server does the work; this only
// names the zip and where the tree goes.
func notionImportOp(bucket Arg) Op {
	return Op{
		Use:   "import-notion <bucket>",
		Short: "Turn a Notion export zip in a bucket into clean Markdown",
		Long: `Reads a Notion export zip (Settings, Export, Markdown and CSV) from a bucket
and writes a clean Markdown tree back to the same bucket: Notion's 32 character
ids are dropped from every name, database CSVs become tables that link to their
row pages, and links between pages point at the new paths. manifest.json lists
every file with its source path and Notion id.

Upload the zip first (miosa storage put). Nothing is written when the zip is
refused. Running it again rewrites the same files.`,
		Example: "  miosa storage import-notion docs --source exports/notion.zip\n  miosa storage import-notion docs --source exports/notion.zip --prefix wiki/",
		Method:  "POST", Path: "/storage/buckets/{0}/imports/notion", Args: []Arg{bucket},
		Flags: []Flag{
			{Name: "source", Usage: "Key of the export zip in the bucket", Field: "source_key", Required: true},
			{Name: "prefix", Usage: "Where the tree goes (default notion/)", Field: "target_prefix"},
		},
		Human: func(cmd *cobra.Command, _ []string, resp any) error {
			get := func(path string) any { return lookupPath(resp, "data."+path) }
			p := printerFor(cmd)
			p.Line("Imported %v files into %v", get("files_written"), get("prefix"))
			p.Line("Pages %v, databases %v, assets %v", get("stats.pages"), get("stats.databases"), get("stats.assets"))
			if n := get("stats.links_unresolved"); n != nil && fmt.Sprint(n) != "0" {
				p.Line("%v links point at pages that are not in the export and were left as they were", n)
			}
			p.Line("Manifest: %v", get("manifest_key"))
			return nil
		},
	}
}
