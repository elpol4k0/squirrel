package repo

import (
	"bufio"
	"fmt"
	"os"

	"golang.org/x/term"
)

func readPassword(prompt string) ([]byte, error) {
	fmt.Print(prompt)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		pw, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return nil, fmt.Errorf("read password: %w", err)
		}
		if len(pw) == 0 {
			return nil, fmt.Errorf("password must not be empty")
		}
		return pw, nil
	}
	// non-terminal fallback for tests and piped input
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	fmt.Println()
	pw := scanner.Bytes()
	if len(pw) == 0 {
		return nil, fmt.Errorf("password must not be empty")
	}
	out := make([]byte, len(pw))
	copy(out, pw)
	return out, nil
}
