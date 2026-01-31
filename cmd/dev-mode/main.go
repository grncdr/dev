package main

import "dev-mode/internal/cli"

func main() {
	cli.ExitErr(cli.Execute())
}
