package ui

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// uiSourceFiles covers authored UI sources and static text. Generated bundles
// are checked through the HTTP handler and browser tests, not as source copy.
func uiSourceFiles(t *testing.T, extensions ...string) []string {
	t.Helper()
	wanted := make(map[string]bool, len(extensions))
	for _, extension := range extensions {
		wanted[extension] = true
	}
	var files []string
	for _, root := range []string{"frontend", "assets"} {
		err := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if filepath.ToSlash(file) == "assets/build" {
					return filepath.SkipDir
				}
				return nil
			}
			if wanted[strings.ToLower(filepath.Ext(file))] {
				files = append(files, filepath.ToSlash(file))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scan UI sources under %s: %v", root, err)
		}
	}
	if len(files) == 0 {
		t.Fatal("no UI source files found")
	}
	return files
}
