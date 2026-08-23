// Package minishell is the smallest thing that can honestly be called
// /bin/sh, and it exists for exactly one caller.
//
// The X server compiles its keyboard map at start-up by running xkbcomp, and
// it does so through the C library's popen, which runs "/bin/sh -c <command>".
// A distroless image has no /bin/sh, so the X server fails with "XKB: Failed
// to compile keymap" followed by "Failed to activate virtual core keyboard",
// and does not start at all. The message names neither the shell nor xkbcomp,
// which is why this comment is long.
//
// The obvious fix is to put a shell in the image. That undoes the reason for
// the image being what it is: a shell turns any bug that can influence a
// command line into arbitrary code execution, and there is nothing else in
// here that wants one.
//
// So the cue binary answers to the name sh. /bin/sh is a symbolic link to it,
// and when it is invoked under that name it will run one simple command:
// split the string into words, honouring quotes, and execute it. It has no
// pipes, no redirection, no variables, no expansion, no substitution, no
// built-ins, no operators and no job control. Anything of the sort is refused
// rather than approximated, because a half-implemented shell that silently
// does the wrong thing would be worse than none.
package minishell

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Name is the argument-zero this package answers to.
const Name = "sh"

// IsInvokedAsShell reports whether the process was started under the name of
// the shell, which is the only time any of this happens.
func IsInvokedAsShell(argumentZero string) bool {
	base := argumentZero
	if index := strings.LastIndexByte(base, '/'); index >= 0 {
		base = base[index+1:]
	}
	return base == Name
}

// Run executes "sh -c <command>" and does not return: the command replaces
// this process, so that the file descriptors popen set up — the pipe the X
// server writes the keymap into — reach it unchanged.
//
// Anything other than the -c form, or a command using a shell feature, exits
// with 127, which is what a shell returns for a command it cannot run.
func Run(arguments []string, errorOutput io.Writer) int {
	command, err := commandFrom(arguments)
	if err != nil {
		fmt.Fprintf(errorOutput, "sh: %s\n", err)
		return 127
	}

	words, err := split(command)
	if err != nil {
		fmt.Fprintf(errorOutput, "sh: %s\n", err)
		return 127
	}
	if len(words) == 0 {
		return 0
	}

	path, err := exec.LookPath(words[0])
	if err != nil {
		fmt.Fprintf(errorOutput, "sh: %s: not found\n", words[0])
		return 127
	}

	// Exec rather than fork: the caller is popen, which is waiting on this
	// process and reading or writing its pipe. Replacing the process keeps
	// both the descriptors and the process identity it is waiting for.
	if err := syscall.Exec(path, words, os.Environ()); err != nil {
		fmt.Fprintf(errorOutput, "sh: %s: %s\n", words[0], err)
		return 126
	}
	return 0
}

// commandFrom picks the command out of the argument list. Only "sh -c
// <command>" is accepted; the interactive and script forms have no caller
// here and would need a shell to implement.
func commandFrom(arguments []string) (string, error) {
	for index := 1; index < len(arguments); index++ {
		switch arguments[index] {
		case "-c":
			if index+1 >= len(arguments) {
				return "", fmt.Errorf("-c wants a command after it")
			}
			return arguments[index+1], nil
		case "-":
			continue
		default:
			if strings.HasPrefix(arguments[index], "-") {
				continue
			}
			return "", fmt.Errorf("this shell runs only \"sh -c <command>\"")
		}
	}
	return "", fmt.Errorf("this shell runs only \"sh -c <command>\"")
}

// forbidden are the characters that mean something to a real shell. Meeting
// one is an error rather than something to pass through, because passing it
// through would run a different command than the caller wrote.
const forbidden = "|&;<>()$`*?[]{}!#~\n"

// split breaks a command line into words, honouring single quotes, double
// quotes and backslash escapes, which is all the quoting the one caller uses:
// it writes the program and the output file in double quotes.
func split(command string) ([]string, error) {
	var words []string
	var current strings.Builder
	started := false

	for index := 0; index < len(command); index++ {
		character := command[index]

		switch character {
		case ' ', '\t':
			if started {
				words = append(words, current.String())
				current.Reset()
				started = false
			}
			continue

		case '\'':
			started = true
			closing := strings.IndexByte(command[index+1:], '\'')
			if closing < 0 {
				return nil, fmt.Errorf("a single quote is not closed")
			}
			current.WriteString(command[index+1 : index+1+closing])
			index += closing + 1
			continue

		case '"':
			started = true
			for index++; index < len(command); index++ {
				if command[index] == '"' {
					break
				}
				if command[index] == '\\' && index+1 < len(command) {
					index++
					current.WriteByte(command[index])
					continue
				}
				if command[index] == '$' || command[index] == '`' {
					return nil, fmt.Errorf("this shell does not expand %q", command[index])
				}
				current.WriteByte(command[index])
			}
			if index >= len(command) {
				return nil, fmt.Errorf("a double quote is not closed")
			}
			continue

		case '\\':
			started = true
			if index+1 < len(command) {
				index++
				current.WriteByte(command[index])
			}
			continue
		}

		if strings.IndexByte(forbidden, character) >= 0 {
			return nil, fmt.Errorf("this shell does not understand %q; it runs one simple command and nothing else", character)
		}

		started = true
		current.WriteByte(character)
	}

	if started {
		words = append(words, current.String())
	}
	return words, nil
}
