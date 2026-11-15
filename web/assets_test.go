package web

import (
	"io/fs"
	"testing"
)

func TestStaticFS_RequiredAssetsExist(t *testing.T) {
	if StaticFS == nil {
		t.Fatal("StaticFS is nil")
	}

	required := []string{
		"vendor/htmx.min.js",
		"vendor/alpine.min.js",
		"vendor/Sortable.min.js",
		"vendor/html5-qrcode.min.js",
		"js/toast.js",
		"js/barcode_scanner.js",
		"css/output.css",
	}

	for _, name := range required {
		data, err := fs.ReadFile(StaticFS, name)
		if err != nil {
			t.Errorf("fs.ReadFile(StaticFS, %q) failed: %v", name, err)
			continue
		}
		if len(data) == 0 {
			t.Errorf("embedded asset %q is empty", name)
		}
	}
}
