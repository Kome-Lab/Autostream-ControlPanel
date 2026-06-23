package database

import "testing"

func TestNormalizeMySQLDSN(t *testing.T) {
	got, err := NormalizeMySQLDSN("mysql://" + "autostream:" + "secret" + "@tcp(db.example.com:3306)/autostream_control_panel")
	if err != nil {
		t.Fatal(err)
	}
	want := "autostream:" + "secret" + "@tcp(db.example.com:3306)/autostream_control_panel?parseTime=true"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeRejectsUnsupportedScheme(t *testing.T) {
	if _, err := NormalizeMySQLDSN("postgres://" + "user:" + "pass" + "@example/db"); err == nil {
		t.Fatal("expected unsupported scheme to fail")
	}
}

func TestMaskDSN(t *testing.T) {
	got := MaskDSN("mysql://" + "autostream:" + "secret" + "@tcp(db.example.com:3306)/autostream_control_panel?parseTime=true")
	if got != "autostream:****@tcp(db.example.com:3306)/autostream_control_panel?parseTime=true" {
		t.Fatalf("unexpected mask: %s", got)
	}
}
