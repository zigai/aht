package processinfo

// ChildRoleEnv marks helper processes launched by aht so inventory does not
// mistake their command arguments for an interactive harness.
const ChildRoleEnv = "AHT_PROCESS_ROLE"
