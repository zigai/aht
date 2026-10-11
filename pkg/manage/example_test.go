package manage_test

import (
	"fmt"

	"github.com/zigai/aht/v2/pkg/manage"
)

func ExampleInstallableHarnesses() {
	fmt.Println(len(manage.InstallableHarnesses()) > 0)
	// Output: true
}
