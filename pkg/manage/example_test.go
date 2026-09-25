package manage_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/manage"
)

func ExampleSupportedHarnesses() {
	fmt.Println(len(manage.SupportedHarnesses()) > 0)
	// Output: true
}
