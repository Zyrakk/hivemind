package directive

import "testing"

func TestParseRouting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		input      string
		wantDir    string
		wantProj   string
		wantParsed bool
	}{
		{
			name:       "spanish prefix with directive label",
			input:      "Proyecto: nhi-watch. Directriz: Implementar carga de YAML",
			wantDir:    "Implementar carga de YAML",
			wantProj:   "nhi-watch",
			wantParsed: true,
		},
		{
			name:       "english prefix with directive label",
			input:      "Project: NHI-WATCH. Directive: Add audit command",
			wantDir:    "Add audit command",
			wantProj:   "NHI-WATCH",
			wantParsed: true,
		},
		{
			name:       "prefix without directive label",
			input:      "Project: flux. Implement health endpoint checks",
			wantDir:    "Implement health endpoint checks",
			wantProj:   "flux",
			wantParsed: true,
		},
		{
			name:       "no routing prefix",
			input:      "Implement scheduler retries",
			wantDir:    "Implement scheduler retries",
			wantProj:   "",
			wantParsed: false,
		},
		{
			name:       "routing with inline directive label without dot",
			input:      "Proyecto: nhi-watch Directriz: mejorar parser",
			wantDir:    "mejorar parser",
			wantProj:   "nhi-watch",
			wantParsed: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotDir, gotProj, gotParsed := ParseRouting(tc.input)
			if gotDir != tc.wantDir {
				t.Fatalf("directive mismatch: got %q want %q", gotDir, tc.wantDir)
			}
			if gotProj != tc.wantProj {
				t.Fatalf("project mismatch: got %q want %q", gotProj, tc.wantProj)
			}
			if gotParsed != tc.wantParsed {
				t.Fatalf("parsed mismatch: got %t want %t", gotParsed, tc.wantParsed)
			}
		})
	}
}
