package main

import "os"

// lockService opens the lock file. The Windows service manager starts only 1 copy of a service,
// so this version does not lock the file. Then uninstall does not wait for the end of the service.
// A later version adds the Windows service.
func lockService(path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return func() { file.Close() }, nil
}
