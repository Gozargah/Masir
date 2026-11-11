// pack-geo prepares the verified desktop geo resources once for all build targets.
package main

import (
	"fmt"
	"os"

	"github.com/Gozargah/Masir/libs/masir-core/geopack"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: pack-geo source empty-destination")
		os.Exit(1)
	}
	if err := geopack.Pack(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
