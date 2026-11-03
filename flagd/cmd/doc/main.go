package main

import (
	"log"

	"github.com/open-feature-forking/openfeature-relay/runtime/cmd"
)

const docPath = "../docs/reference/flagd-cli"

func main() {
	if err := cmd.GenerateDoc(docPath); err != nil {
		log.Fatal(err)
	}
}
