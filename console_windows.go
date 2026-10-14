package main

// getConsoleUserID returns -1 on Windows. A later version starts the agent at the login through the Run key.
func getConsoleUserID() int {
	return -1
}
