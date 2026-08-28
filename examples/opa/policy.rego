package mcp.authz

import rego.v1

# data.policies[<group id>].tools is a list of "<server>/<tool>" glob patterns
# (see data.json). A group with no matching pattern grants nothing.

# input (tools/call, decisionPath.call):
#   {"user": "...", "groups": [...], "server": "...", "tool": "..."}
default allow := false

allow if {
	some group in input.groups
	some pattern in data.policies[group].tools
	glob.match(pattern, ["/"], sprintf("%s/%s", [input.server, input.tool]))
}

# input (tools/list, decisionPath.list):
#   {"user": "...", "groups": [...], "tools": [{"server": "...", "name": "..."}, ...]}
allowed_tools contains t if {
	some t in input.tools
	some group in input.groups
	some pattern in data.policies[group].tools
	glob.match(pattern, ["/"], sprintf("%s/%s", [t.server, t.name]))
}

# data.policies[<group id>].catalog is a plain boolean, independent of the
# tools glob patterns above.

# input (GET /mcp/list?tools=true, decisionPath.catalog):
#   {"user": "...", "groups": [...]}
default allow_catalog := false

allow_catalog if {
	some group in input.groups
	data.policies[group].catalog == true
}
