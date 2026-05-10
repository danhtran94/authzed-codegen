package authz

# Sample policy mixing three authorization paradigms:
#   - RBAC: a static "admin" role grants everything
#   - ReBAC: SpiceDB's folder.browse permission via the generated builtin
#   - deny override: a blocklist that overrides any allow
#
# The SpiceDB builtin (extsvc.check_folder_browse) is registered by the
# embedding Go program via extsvc.SpiceDBBuiltins(engine, ctx). It takes
# a "type:id" subject string, a resource id, and a caveat-context object
# ({} when no caveat applies).

default allow := false

# deny set — any non-empty deny overrides every allow.
deny contains msg if {
	input.user.id == "banned-user"
	msg := sprintf("user %q is blocklisted", [input.user.id])
}

# RBAC leg.
granted if input.user.role == "admin"

# ReBAC leg — consult SpiceDB's relationship graph.
granted if extsvc.check_folder_browse(
	sprintf("extsvc/user:%s", [input.user.id]),
	input.resource.id,
	{},
)

allow if {
	count(deny) == 0
	granted
}
