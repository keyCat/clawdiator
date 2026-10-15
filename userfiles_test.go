package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestFiles writes each file with its folders. The content is the name of the file.
func writeTestFiles(t *testing.T, paths ...string) {
	t.Helper()
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(filepath.Base(path)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// The removal runs as root in folders that the users control. It must remove only the known files of clawdiator.
// A symbolic link of a user must not lead it to other files.
func TestRemoveUserFiles(t *testing.T) {
	base := t.TempDir()
	usersDir, outside := filepath.Join(base, "Users"), filepath.Join(base, "outside")
	appDir := func(user string) string { return filepath.Join(usersDir, user, userAppDir) }

	// alice has only the known files. Her folder must go, but "Application Support" must stay.
	writeTestFiles(t, filepath.Join(appDir("alice"), "agent.json"), filepath.Join(appDir("alice"), "agent.json.tmp"),
		filepath.Join(appDir("alice"), "agent.log"), filepath.Join(appDir("alice"), "agent.log.1"))
	// bob also has his own file. Only the known files must go.
	writeTestFiles(t, filepath.Join(appDir("bob"), "agent.json"), filepath.Join(appDir("bob"), "notes.txt"))
	// carol has no folder of clawdiator.
	writeTestFiles(t, filepath.Join(usersDir, "carol", "Library", "Preferences", "settings.plist"))
	// A file in the users folder is not a home folder.
	writeTestFiles(t, filepath.Join(usersDir, ".localized"))
	// The home folder of dave is a link to another folder.
	writeTestFiles(t, filepath.Join(outside, "dave", userAppDir, "agent.json"))
	mustLink(t, filepath.Join(outside, "dave"), filepath.Join(usersDir, "dave"))
	// The "Application Support" folder of erin is a link out of her home folder.
	writeTestFiles(t, filepath.Join(outside, "erin", appName, "agent.json"))
	mustLink(t, filepath.Join(outside, "erin"), filepath.Join(usersDir, "erin", "Library", "Application Support"))
	// The folder of clawdiator of frank is a relative link to his own documents. os.Root follows such a link.
	writeTestFiles(t, filepath.Join(usersDir, "frank", "Documents", "project", "agent.json"))
	mustLink(t, filepath.Join("..", "..", "Documents", "project"), appDir("frank"))

	kept, err := removeUserFiles(usersDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, folder := range kept {
		name := strings.Split(strings.TrimPrefix(folder.Path, usersDir+string(filepath.Separator)), string(filepath.Separator))[0]
		names = append(names, name)
		if (name == "bob") != (folder.Err == nil) {
			t.Errorf("only bob must have a folder without an error: %s, %v", name, folder.Err)
		}
	}
	if strings.Join(names, ",") != "bob,erin,frank" {
		t.Errorf("the kept folders = %v", names)
	}

	if _, err := os.Stat(appDir("alice")); !os.IsNotExist(err) {
		t.Errorf("the folder of alice must go: %v", err)
	}
	for _, path := range []string{
		filepath.Join(usersDir, "alice", "Library", "Application Support"),
		filepath.Join(appDir("bob"), "notes.txt"),
		filepath.Join(usersDir, "carol", "Library", "Preferences", "settings.plist"),
		filepath.Join(outside, "dave", userAppDir, "agent.json"),
		filepath.Join(outside, "erin", appName, "agent.json"),
		filepath.Join(usersDir, "frank", "Documents", "project", "agent.json"),
		appDir("frank"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s must stay: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(appDir("bob"), "agent.json")); !os.IsNotExist(err) {
		t.Errorf("the known file of bob must go: %v", err)
	}
	if kept, err := removeUserFiles(filepath.Join(base, "missing")); kept != nil || err != nil {
		t.Errorf("a missing users folder must give no result: %v, %v", kept, err)
	}
}

func TestUninstallRemovesUserFiles(t *testing.T) {
	in, _, stdout := newTestInstaller(t, "yes\n")
	if err := in.commandInstall(nil); err != nil {
		t.Fatal(err)
	}
	alice := filepath.Join(in.paths.UsersDir, "alice", userAppDir)
	bob := filepath.Join(in.paths.UsersDir, "bob", userAppDir)
	writeTestFiles(t, filepath.Join(alice, "agent.json"), filepath.Join(alice, "agent.log"), filepath.Join(bob, "agent.json"), filepath.Join(bob, "notes.txt"))
	stdout.Reset()
	if err := in.commandUninstall(); err != nil {
		t.Fatalf("%v\n%s", err, stdout.String())
	}
	if _, err := os.Stat(alice); !os.IsNotExist(err) {
		t.Errorf("uninstall must remove the folder of each user: %v", err)
	}
	for _, want := range []string{"OK      remove the files of the notices in the home folders of the users", bob + " has other files, so the tool kept the folder."} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("the output must contain %q:\n%s", want, stdout.String())
		}
	}
}
