// record-geo records the raw geo pair included in a standalone CLI archive.
package main

import (
	"fmt"
	"os"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: record-geo directory")
		os.Exit(1)
	}
	if err := geopack.Record(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
