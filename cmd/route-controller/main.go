package main

import (
	"fmt"
	"os"

	"github.com/zijiren233/route-controller/internal/app"
	"github.com/zijiren233/route-controller/internal/cli"
)

func main() {
	if err := cli.Execute(app.Run); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)

		os.Exit(1)
	}
}
