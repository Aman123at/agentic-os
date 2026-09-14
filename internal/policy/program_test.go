package policy

import "testing"

func TestProgramLooksThroughWrappersAndSetup(t *testing.T) {
	for cmd, want := range map[string]string{
		"make -j4":                             "make",
		"cd build && make":                     "make",
		"sudo -u root apt-get install -y x":    "apt-get",
		"env FOO=1 timeout 10 ./test.sh":       "test.sh",
		"export X=1; python3 -m pip install x": "python3",
		"FOO=bar /usr/bin/npm test":            "npm",
		"nohup nice -n 5 node server.js &":     "node",
		"source .venv/bin/activate && pytest":  "pytest",
		"$CMD --x":                             "",
		"cd /tmp":                              "",
		`echo "unterminated`:                   "echo",
	} {
		if got := Program(cmd); got != want {
			t.Errorf("Program(%q) = %q, want %q", cmd, got, want)
		}
	}
}
