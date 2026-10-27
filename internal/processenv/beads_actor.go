package processenv

import (
	"os"
	"os/exec"
	"strings"
)

// ResolveBeadsActor follows bd's fallback order: BEADS_ACTOR, BD_ACTOR,
// git user.name, then USER. Values are trimmed, and gitUserName is called only
// when both environment actors are empty. It returns "" if no actor is found,
// leaving the caller's default intact.
func ResolveBeadsActor(getenv func(string) string, gitUserName func() string) string {
	for _, key := range []string{"BEADS_ACTOR", "BD_ACTOR"} {
		if actor := strings.TrimSpace(getenv(key)); actor != "" {
			return actor
		}
	}
	if actor := strings.TrimSpace(gitUserName()); actor != "" {
		return actor
	}
	return strings.TrimSpace(getenv("USER"))
}

// BeadsActor resolves the process actor in bd's fallback order: BEADS_ACTOR,
// BD_ACTOR, git config user.name, then USER. Git is run only when both environment
// actors are empty; a git error leaves the name empty so resolution can continue.
// It returns "" if no actor is found, leaving the caller's default intact.
func BeadsActor() string {
	return ResolveBeadsActor(os.Getenv, func() string {
		name, err := exec.Command("git", "config", "user.name").Output()
		if err != nil {
			return ""
		}
		return string(name)
	})
}
