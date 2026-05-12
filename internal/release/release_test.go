package release

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckForUpdateFromNewerVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Release{
			TagName: "v0.2.0",
			Assets:  []Asset{},
		})
	}))
	defer srv.Close()

	msg, err := CheckForUpdateFrom("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "new update available: v0.2.0 | to update run `rally update`"
	if msg != want {
		t.Fatalf("message = %q, want %q", msg, want)
	}
}

func TestCheckForUpdateFromSameVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Release{
			TagName: "v0.1.0",
			Assets:  []Asset{},
		})
	}))
	defer srv.Close()

	msg, err := CheckForUpdateFrom("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "" {
		t.Fatalf("expected no message, got %q", msg)
	}
}

func TestCheckForUpdateFromServerInternalError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := CheckForUpdateFrom("0.1.0", srv.URL)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestCheckForUpdateFromUnreachableServer(t *testing.T) {
	_, err := CheckForUpdateFrom("0.1.0", "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestCheckForUpdateFromDevVersion(t *testing.T) {
	msg, err := CheckForUpdateFrom("dev", "http://unused")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "" {
		t.Fatalf("expected no message for dev version, got %q", msg)
	}
}

func TestFetchLatestFromValidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if accept := r.Header.Get("Accept"); accept != "application/vnd.github+json" {
			t.Errorf("Accept header = %q, want %q", accept, "application/vnd.github+json")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Release{
			TagName: "v1.2.3",
			Assets: []Asset{
				{Name: "rally_linux_amd64.tar.gz", BrowserDownloadURL: "http://example.com/rally_linux_amd64.tar.gz"},
			},
		})
	}))
	defer srv.Close()

	rel, err := FetchLatestFrom(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Fatalf("TagName = %q, want %q", rel.TagName, "v1.2.3")
	}
	if len(rel.Assets) != 1 {
		t.Fatalf("len(Assets) = %d, want 1", len(rel.Assets))
	}
	if rel.Assets[0].Name != "rally_linux_amd64.tar.gz" {
		t.Fatalf("Asset name = %q, want %q", rel.Assets[0].Name, "rally_linux_amd64.tar.gz")
	}
}

func TestFetchLatestFromMissingTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(Release{})
	}))
	defer srv.Close()

	_, err := FetchLatestFrom(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for missing tag_name")
	}
}

func TestDisplayVersion(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"explicit tag with v prefix", "v0.7.4", "v0.7.4"},
		{"explicit tag without v prefix", "0.7.4", "v0.7.4"},
		{"whitespace trimmed", "  v1.2.3 ", "v1.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayVersion(tt.value); got != tt.want {
				t.Errorf("DisplayVersion(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestDisplayVersionDevFallsBackToEmbedded(t *testing.T) {
	// "dev" should resolve to the embedded VERSION with a -dev suffix.
	got := DisplayVersion("dev")
	if got == "dev" {
		t.Fatalf("DisplayVersion(\"dev\") = %q, expected embedded fallback like \"vX.Y.Z-dev\"", got)
	}
	if got[:1] != "v" || got[len(got)-4:] != "-dev" {
		t.Errorf("DisplayVersion(\"dev\") = %q, want format vX.Y.Z-dev", got)
	}
}
