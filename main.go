package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/golang"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/java"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/python"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/rust"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/typescript"
	"github.com/derek-shaw/agent-work-visualizer/internal/server"
)

const defaultPort = 7777

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	root, port, err := parseArguments(arguments)
	if err != nil {
		return err
	}
	a := analyzer.New()
	for _, language := range []analyzer.Language{
		python.New(), golang.New(), java.New(), rust.New(), typescript.New(),
	} {
		if err := a.Register(language); err != nil {
			return err
		}
	}
	return server.Run(a, root, port)
}

// parseArguments returns ROOT, or "" if the arguments name no ROOT, and the
// port.
func parseArguments(arguments []string) (root string, port int, err error) {
	usage := errors.New("usage: agent-work-visualizer [ROOT] [--port N]")
	port = defaultPort
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--port":
			index++
			if index >= len(arguments) {
				return "", 0, errors.New("--port requires a number")
			}
			port, err = strconv.Atoi(arguments[index])
			if err != nil || port < 0 || port > 65535 {
				return "", 0, fmt.Errorf("invalid port %q", arguments[index])
			}
		case "-h", "--help":
			return "", 0, usage
		default:
			if len(arguments[index]) > 0 && arguments[index][0] == '-' {
				return "", 0, fmt.Errorf("unknown option %s", arguments[index])
			}
			if root != "" {
				return "", 0, errors.New("at most one ROOT is allowed")
			}
			root = arguments[index]
		}
	}
	return root, port, nil
}
