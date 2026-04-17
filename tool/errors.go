package tool

import "fmt"

func ParseError(name, err, input string) error {
	return fmt.Errorf("%s: parse error: %s", name, err)
}

func WrapError(name string, err error) error {
	return fmt.Errorf("%s: %w", name, err)
}

func ExecError(name, cmd string, code int, output string) error {
	return fmt.Errorf("%s: exit %d", name, code)
}
