package main

import (
	"errors"
	"os"
	"path/filepath"
)

// userAppDir is the folder of clawdiator in the home folder of a user on macOS. getUserAppDir gives the same folder.
var userAppDir = filepath.Join("Library", "Application Support", appName)

// userFileNames are all files that the notice agent and the tray write in the folder of a user.
var userFileNames = []string{"agent.json", "agent.json.tmp", "agent.log", "agent.log.1"}

// userFolderResult is the result for 1 folder that removeUserFiles did not remove.
type userFolderResult struct {
	Path string
	// Err is nil if the folder has other files. Then the folder stays.
	Err error
}

// removeUserFiles removes the folder of clawdiator in the home folder of each user in usersDir.
// The removal runs as root in folders that the users control, so it has these limits:
//   - It removes only the files in userFileNames, then the folder only if it is empty. A folder with other files stays.
//   - It does each removal through os.Root of the home folder. A symbolic link cannot lead it out of the home folder.
//   - It skips a home folder that is a symbolic link, and a folder of clawdiator that is a symbolic link.
//
// The result names each folder that stays.
func removeUserFiles(usersDir string) ([]userFolderResult, error) {
	entries, err := os.ReadDir(usersDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var kept []userFolderResult
	for _, entry := range entries {
		// ReadDir does not follow links. A link has the type ModeSymlink, so IsDir is false for it.
		if !entry.IsDir() {
			continue
		}
		home := filepath.Join(usersDir, entry.Name())
		isRemoved, err := removeUserAppDir(home)
		if err != nil || !isRemoved {
			kept = append(kept, userFolderResult{Path: filepath.Join(home, userAppDir), Err: err})
		}
	}
	return kept, nil
}

// removeUserAppDir removes the known files and the empty folder of clawdiator in 1 home folder.
// The result is true if the folder is gone, also if it was not there.
func removeUserAppDir(home string) (bool, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Lstat(userAppDir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, errors.New("it is not a folder")
	}
	for _, name := range userFileNames {
		if err := root.Remove(filepath.Join(userAppDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
	}
	// Remove does not remove a folder with files. So a folder with other files stays.
	if err := root.Remove(userAppDir); err != nil {
		return false, nil
	}
	return true, nil
}
