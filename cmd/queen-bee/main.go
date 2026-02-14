package main

import (
	"context"
	"log"
	"os"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags)

	app := newApp()
	if err := app.Run(context.Background(), os.Args); err != nil {
		logger.Fatal(err)
	}
}
