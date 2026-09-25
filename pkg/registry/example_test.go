package registry_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/registry"
)

func ExampleNormalizeActivity() {
	activity, err := registry.NormalizeActivity("waiting")
	if err != nil {
		panic(err)
	}

	fmt.Println(activity)
	// Output: waiting
}
