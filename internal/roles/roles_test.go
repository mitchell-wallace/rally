package roles

import (
	"reflect"
	"testing"
)

func TestBuiltinsStableOrder(t *testing.T) {
	builtins := Builtins()
	got := make([]string, 0, len(builtins))
	for _, spec := range builtins {
		got = append(got, spec.Name)
		if spec.Name == "ui" {
			t.Fatalf("Builtins() included ui tombstone")
		}
	}

	want := []string{"intern", "junior", "senior", "architect", "review", "verify", "qa", "recovery"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Builtins() names = %v, want %v", got, want)
	}
}

func TestLookup(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		wantName           string
		wantOK             bool
		wantGenerated      bool
		checkGeneratedFlag bool
	}{
		{
			name:     "case insensitive",
			input:    "SENIOR",
			wantName: "senior",
			wantOK:   true,
		},
		{
			name:     "trims whitespace",
			input:    " Verify ",
			wantName: "verify",
			wantOK:   true,
		},
		{
			name:     "reviewer alias",
			input:    "reviewer",
			wantName: "review",
			wantOK:   true,
		},
		{
			name:               "ui tombstone",
			input:              "ui",
			wantName:           "ui",
			wantOK:             true,
			wantGenerated:      false,
			checkGeneratedFlag: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if got.Name != tt.wantName {
				t.Fatalf("Lookup(%q) Name = %q, want %q", tt.input, got.Name, tt.wantName)
			}
			if tt.checkGeneratedFlag && got.GeneratedByDefault != tt.wantGenerated {
				t.Fatalf("Lookup(%q) GeneratedByDefault = %v, want %v", tt.input, got.GeneratedByDefault, tt.wantGenerated)
			}
		})
	}
}

func TestLookupUnknownRoleFallback(t *testing.T) {
	got, ok := Lookup(" MyRole ")
	if ok {
		t.Fatalf("Lookup unknown ok = true, want false")
	}
	if got.Name != "myrole" {
		t.Fatalf("Lookup unknown Name = %q, want %q", got.Name, "myrole")
	}
	if got.Mode != ModeImplement {
		t.Fatalf("Lookup unknown Mode = %q, want %q", got.Mode, ModeImplement)
	}
	if got.WritePolicy != PolicyImplementation {
		t.Fatalf("Lookup unknown WritePolicy = %q, want %q", got.WritePolicy, PolicyImplementation)
	}
}

func TestSuggest(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "review typo",
			input: "reveiw",
			want:  "review",
		},
		{
			name:  "verify typo",
			input: "vrify",
			want:  "verify",
		},
		{
			name:  "custom role",
			input: "myrole",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Suggest(tt.input); got != tt.want {
				t.Fatalf("Suggest(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuiltinsHaveRequiredFields(t *testing.T) {
	for _, spec := range Builtins() {
		t.Run(spec.Name, func(t *testing.T) {
			if spec.Summary == "" {
				t.Fatalf("Summary is empty")
			}
			if spec.Mode == "" {
				t.Fatalf("Mode is empty")
			}
			if spec.WritePolicy == "" {
				t.Fatalf("WritePolicy is empty")
			}
			if len(spec.DefaultRoute) == 0 {
				t.Fatalf("DefaultRoute is empty")
			}
		})
	}
}
