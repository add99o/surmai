# Surmai Travel private plugin

Run `python plugin/package.py` after installing `jsonschema` and `PyYAML`.
The archive is written outside the source directory under `dist/`.

The remote MCP server is part of Surmai's existing Go application. It is disabled
unless `SURMAI_MCP_ENABLED=true`. Configure its canonical HTTPS `/mcp` URL,
Auth0 issuer (including the trailing slash), and an explicit JSON map from Auth0
subject IDs to normal PocketBase user record IDs. No client secret, user password,
superuser token, or Auth0 Management API token is required by the runtime.

The server checks RS256 signatures against Auth0's rotating JWKS, issuer,
audience, expiry, not-before, subject mapping, scopes, and existing record rules.
The package uses OAuth discovery. The Auth0 API uses per-app authorization, so
the exact dynamically registered host client needs the appropriate delegated
Surmai permissions when the connection is established. Do not authorize an older
ChatGPT client by guessing its name. Keep client-credentials access separate.

Seven tools are exposed: get_profile, list_trips, get_trip_itinerary,
list_expenses, list_attachments, propose_itinerary_changes, and get_proposal.
The only MCP write creates an expiring proposal. A human approves it through the
native Surmai Assistant tab; this path checks record permissions, rejects stale
baselines, and commits the changes and approval status in one transaction.
This path makes no AI API call. Expense edits and attachment downloads/uploads
are excluded, and traveller profile/passport fields are not exported.

See `deployment/surmai-mcp/README.md` for staged deployment and rollback.
Do not claim the plugin is connected until a real OAuth-linked read succeeds.
