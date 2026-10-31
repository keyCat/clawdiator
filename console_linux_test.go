package main

import "testing"

func TestParseGraphicalSession(t *testing.T) {
	for _, testCase := range []struct {
		properties string
		wantUser   int
		wantFound  bool
	}{
		{"Type=wayland\nActive=yes\nUser=1000\n", 1000, true},
		{"User=1001\nType=x11\nActive=yes", 1001, true},
		{"  Type=wayland\r\n  Active=yes\r\n  User=1000\r\n", 1000, true},
		// A text console, an inactive session, root and a session without a user start no agent.
		{"Type=tty\nActive=yes\nUser=1000\n", 1000, false},
		{"Type=wayland\nActive=no\nUser=1000\n", 1000, false},
		{"Type=x11\nActive=yes\nUser=0\n", 0, false},
		{"Type=wayland\nActive=yes\n", 0, false},
		{"Type=wayland\nActive=yes\nUser=ilya\n", 0, false},
		{"", 0, false},
	} {
		userID, isFound := parseGraphicalSession(testCase.properties)
		if isFound != testCase.wantFound || (isFound && userID != testCase.wantUser) {
			t.Errorf("%q = %d, %v, want %d, %v", testCase.properties, userID, isFound, testCase.wantUser, testCase.wantFound)
		}
	}
}
