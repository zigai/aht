package harness

type Distribution struct {
	Source, Package, Repo, Asset, URL, MaxVersion, Directory, Family, PackageExtras string
	Install                                                                         func(string, string, string) []DistributionCommand
}
type DistributionCommand struct {
	Name string
	Args []string
	Env  []string
}

func DownloadCommand(url, path string) DistributionCommand {
	return DistributionCommand{Name: "curl", Args: []string{"--fail", "--silent", "--show-error", "--location", "--retry", "3", "--max-time", "180", "--output", path, url}, Env: nil}
}
