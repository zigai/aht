package registry

const (
	harnessCatalog   Harness = "catalog"
	harnessPrimary   Harness = "primary"
	harnessOther     Harness = "other"
	harnessSecondary Harness = "secondary"
	harnessAlternate Harness = "alternate"
	harnessOrdered   Harness = "ordered"
	harnessShared    Harness = "shared"
)

const heldTurnEvent = "turn_end"

type fixtureRules struct{ authority Authority }

func (fixtureRules) SamePane(a Location, b Location) bool { return !a.Empty() && a == b }

func (fixtureRules) Known(id Harness) bool {
	return id != "" && id != "invalid" && id != "unknown" && id != "not-a-harness" &&
		id != "catalog-code"
}

func (r fixtureRules) Policy(id Harness) Policy {
	authority := r.authority
	if authority == "" {
		authority = AuthorityHook
	}

	reporter := string(id) + "-extension"

	return Policy{
		Reporter:         reporter,
		ExclusiveProcess: id != harnessShared,
		Authority:        authority,
		ScreenFallback:   true,
		CatalogCreates:   id == harnessCatalog,
		HeldTurnEvents:   []string{heldTurnEvent},
	}
}
