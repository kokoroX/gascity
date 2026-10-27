package processenv

import "testing"

func TestResolveBeadsActor(t *testing.T) {
	for _, tc := range []struct {
		name       string
		beadsActor string
		bdActor    string
		gitName    string
		user       string
		want       string
		wantGit    bool
	}{
		{
			name:       "BEADS_ACTOR takes precedence",
			beadsActor: "beads-actor",
			bdActor:    "bd-actor",
			gitName:    "Git User",
			user:       "shell-user",
			want:       "beads-actor",
		},
		{
			name:       "BEADS_ACTOR is trimmed",
			beadsActor: " \tbeads-actor\n",
			want:       "beads-actor",
		},
		{
			name:       "whitespace BEADS_ACTOR falls back to trimmed BD_ACTOR",
			beadsActor: " \t\n",
			bdActor:    " \tbd-actor\n",
			gitName:    "Git User",
			user:       "shell-user",
			want:       "bd-actor",
		},
		{
			name:    "BD_ACTOR fallback",
			bdActor: "bd-actor",
			user:    "shell-user",
			want:    "bd-actor",
		},
		{
			name:    "git fallback",
			gitName: "Git User",
			user:    "shell-user",
			want:    "Git User",
			wantGit: true,
		},
		{
			name:       "whitespace env actors fall back to trimmed git name",
			beadsActor: " \t",
			bdActor:    "\n ",
			gitName:    " \tGit User\n",
			user:       "shell-user",
			want:       "Git User",
			wantGit:    true,
		},
		{
			name:    "USER fallback",
			user:    "shell-user",
			want:    "shell-user",
			wantGit: true,
		},
		{
			name:    "whitespace git name falls back to trimmed USER",
			gitName: " \t\n",
			user:    " \tshell-user\n",
			want:    "shell-user",
			wantGit: true,
		},
		{
			name:    "all empty",
			wantGit: true,
		},
		{
			name:       "all whitespace",
			beadsActor: " \t\n",
			bdActor:    " \t\n",
			gitName:    " \t\n",
			user:       " \t\n",
			wantGit:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{
				"BEADS_ACTOR": tc.beadsActor,
				"BD_ACTOR":    tc.bdActor,
				"USER":        tc.user,
			}
			gitCalls := 0
			got := ResolveBeadsActor(func(key string) string { return env[key] }, func() string {
				if !tc.wantGit {
					t.Fatal("gitUserName called despite a non-empty environment actor")
				}
				gitCalls++
				return tc.gitName
			})
			if got != tc.want {
				t.Errorf("ResolveBeadsActor() = %q, want %q", got, tc.want)
			}
			wantCalls := 0
			if tc.wantGit {
				wantCalls = 1
			}
			if gitCalls != wantCalls {
				t.Errorf("gitUserName called %d times, want %d", gitCalls, wantCalls)
			}
		})
	}
}
