package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/woodybriggs/simpllm/internal/secret"
)

func secretCmd(args []string) {
	if len(args) < 1 {
		printSecretUsage()
		return
	}

	switch args[0] {
	case "set":
		secretSet(args[1:])
	case "get":
		secretGet(args[1:])
	case "delete", "rm":
		secretDelete(args[1:])
default:
		fmt.Fprintf(os.Stderr, "unknown secret command: %s\n\n", args[0])
		printSecretUsage()
		os.Exit(1)
	}
}

func printSecretUsage() {
	fmt.Fprintln(os.Stderr, "usage: simpllm secret <command>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  set <name>       store a secret (prompts for value)")
	fmt.Fprintln(os.Stderr, "  get <name>       retrieve a secret")
	fmt.Fprintln(os.Stderr, "  delete <name>    remove a secret")

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "examples:")
	fmt.Fprintln(os.Stderr, "  simpllm secret set OPENAI_API_KEY")
	fmt.Fprintln(os.Stderr, "  simpllm secret get OPENAI_API_KEY")

}

func secretSet(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: simpllm secret set <name>")
		os.Exit(1)
	}
	name := args[0]

	// Read value from stdin.
	fmt.Fprintf(os.Stderr, "Enter secret value for %s: ", name)
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading input: %v\n", err)
		os.Exit(1)
	}
	value = strings.TrimRight(value, "\r\n")

	if value == "" {
		fmt.Fprintln(os.Stderr, "error: empty value")
		os.Exit(1)
	}

	if err := secret.Set(name, value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("stored %s\n", name)
}

func secretGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: simpllm secret get <name>")
		os.Exit(1)
	}
	name := args[0]

	value, err := secret.Get(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(value)
}

func secretDelete(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: simpllm secret delete <name>")
		os.Exit(1)
	}
	name := args[0]

	if err := secret.Delete(name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("deleted %s\n", name)
}

