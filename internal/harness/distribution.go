package harness

const (
	DistributionNPM     DistributionSource = "npm"
	DistributionPyPI    DistributionSource = "pypi"
	DistributionGitHub  DistributionSource = "github"
	DistributionChannel DistributionSource = "channel"
	// DistributionWeekly harnesses have no queryable release feed and are
	// checked on a schedule instead of per release.
	DistributionWeekly DistributionSource = "weekly"
)

const (
	FamilyTreeExtension DistributionFamily = "tree-extension"
	FamilyPartPlugin    DistributionFamily = "part-plugin"
)

// DistributionSource names where release versions of a harness are published.
type DistributionSource string

// DistributionFamily groups harnesses that share integration code, so a change
// to one rechecks the others.
type DistributionFamily string

//exhaustruct:optional
type Distribution struct {
	Source                                                          DistributionSource
	Family                                                          DistributionFamily
	Package, Repo, Asset, URL, MaxVersion, Directory, PackageExtras string
	Install                                                         func(
		string,
		string,
		string,
	) []DistributionCommand
}

type DistributionCommand struct {
	Name string
	Args []string
	Env  []string
}

func DownloadCommand(url string, path string) DistributionCommand {
	return DistributionCommand{
		Name: "curl",
		Args: []string{
			"--fail",
			"--silent",
			"--show-error",
			"--location",
			"--retry",
			"3",
			"--max-time",
			"180",
			"--output",
			path,
			url,
		},
		Env: nil,
	}
}
