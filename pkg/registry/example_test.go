package registry_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/registry"
)

func ExampleParseActivity() {
	activity, err := registry.ParseActivity("waiting")
	if err != nil {
		panic(err)
	}

	fmt.Println(activity)
	// Output: waiting
}
