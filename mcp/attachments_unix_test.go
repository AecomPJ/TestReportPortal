//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO with no writer must be rejected promptly instead of blocking record_result.
func TestReadAttachmentFIFO(t *testing.T) {
	dir := mustReal(t, t.TempDir())
	fifo := filepath.Join(dir, "pipe.log")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo が使えない環境:", err)
	}
	tl := &tools{cfg: config{attachmentDirs: []string{dir}}}
	done := make(chan error, 1)
	go func() {
		_, _, err := tl.readAttachment(fifo, maxRecordBytes)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "通常のファイルではありません") {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FIFO を開く処理が待機したまま戻らない")
	}
}
