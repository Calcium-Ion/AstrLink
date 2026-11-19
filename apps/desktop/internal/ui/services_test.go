package ui

import "testing"

func TestSavedGatewayFilesOfferANameEverySystemTakes(t *testing.T) {
	for name, want := range map[string]string{
		"image-1.png":                "image-1.png",
		`../a/b\c:d*e?f"g<h>i|j.mp4`: "..abcdefghij.mp4",
		"line\nbreak.png":            "linebreak.png",
		"画像.png":                     "画像.png",
	} {
		if got := safeFilename(name); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", name, got, want)
		}
	}
}
