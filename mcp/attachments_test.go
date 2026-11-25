package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadAttachment(t *testing.T) {
	allowed := mustReal(t, t.TempDir())
	outside := mustReal(t, t.TempDir())
	write := func(dir, name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	inside := write(allowed, "log.txt", "hello")
	secret := write(outside, "secret.txt", "secret")
	if err := os.Symlink(secret, filepath.Join(allowed, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(allowed, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(allowed, "empty.txt", "")
	tl := &tools{cfg: config{attachmentDirs: []string{allowed}}}

	if _, data, err := tl.readAttachment(inside, maxRecordBytes); err != nil || string(data) != "hello" {
		t.Fatalf("inside file: data=%q err=%v", data, err)
	}
	for name, tc := range map[string]struct {
		path   string
		budget int64
		want   string
	}{
		"outside":           {secret, maxRecordBytes, "許可されたディレクトリ"},
		"symlink outside":   {filepath.Join(allowed, "link.txt"), maxRecordBytes, "許可されたディレクトリ"},
		"directory":         {filepath.Join(allowed, "dir"), maxRecordBytes, "通常のファイルではありません"},
		"empty":             {filepath.Join(allowed, "empty.txt"), maxRecordBytes, "空です"},
		"over total budget": {inside, 4, "合計"},
	} {
		if _, _, err := tl.readAttachment(tc.path, tc.budget); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err=%v, want containing %q", name, err, tc.want)
		}
	}
}
