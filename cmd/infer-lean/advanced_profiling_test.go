package main

import "testing"

func TestNSYSAttachArgsFromHelp(t *testing.T) {
	tests := []struct {
		name     string
		helpText string
		pid      int
		want     [][]string
	}{
		{
			name: "supports attach and pid",
			helpText: `
--attach-pid=
--pid=
`,
			pid:  42,
			want: [][]string{{"--attach-pid=42"}, {"--pid=42"}},
		},
		{
			name: "supports only pid",
			helpText: `
--pid=
`,
			pid:  77,
			want: [][]string{{"--pid=77"}},
		},
		{
			name: "supports neither",
			helpText: `
--duration=
--trace=
`,
			pid:  12,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nsysAttachArgsFromHelp(tc.helpText, tc.pid)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d variants, got %d", len(tc.want), len(got))
			}
			for i := range got {
				if len(got[i]) != len(tc.want[i]) {
					t.Fatalf("expected variant %d to have %d args, got %d", i, len(tc.want[i]), len(got[i]))
				}
				for j := range got[i] {
					if got[i][j] != tc.want[i][j] {
						t.Fatalf("expected variant[%d][%d]=%q, got %q", i, j, tc.want[i][j], got[i][j])
					}
				}
			}
		})
	}
}
