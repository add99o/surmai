# Surmai MCP deployment preparation

This is an illustrative override for the **existing** Surmai service, not a
second stack or a complete compose file. Confirm all variable names against the
finished implementation before use. The example identity is fictitious; keep
the real subject-to-normal-user-ID mapping out of Git and shared logs. A
successful Auth0 login alone must not grant access to an unmapped identity.

## Before cutover

1. Verify the implementation and its security tests, then build a same-repo PR
   image from the exact commit. The existing workflow publishes a PR-specific
   tag. Confirm its successful build, pullability on linux/amd64, and registry
   digest; use the digest in the override. Do not dispatch the main/manual
   workflow just to build this: it also publishes `latest`, which production
   currently uses.
2. Record the running container's exact image ID and repository digest and
   preserve a locally available copy or other verified pullable reference to
   that exact image. A mutable `latest` tag is not a rollback artifact. Preserve
   the existing stack configuration privately because it may contain secrets.
3. Confirm the actual service key, volume source and mount destination, port,
   network, restart policy, and proxy route from the existing configuration.
   Dockhand's live stack listing showed one running Surmai container and a
   9497:8080 port mapping; the data source and proxy route were not established.
   Do not expose environment values while inspecting it.
4. Create and verify a consistent backup of the entire PocketBase data
   directory, including uploaded files, using its supported backup facility or
   a coordinated offline snapshot. A plain copy of an active SQLite database
   is not sufficient evidence of a consistent backup. Establish a restore
   procedure and account for any writes since the backup before cutover.
   Dockhand's stack listing does not establish that a backup feature is
   available; verify the chosen method in the live environment.
5. Verify the public reverse proxy already routes the required discovery and
   `/mcp` requests to the existing app, including methods and authorization
   headers. Preserve its other routes and TLS configuration. Confirm the real
   subject mapping privately and enter it through the deployment's existing
   configuration mechanism.

## Cutover and verification

Apply only the equivalent image and MCP environment changes to the existing
service, preserving all existing settings, data mounts and project identity.
The template must use the actual service key, and Dockhand's support for
multiple compose files must be confirmed before attempting to use it directly;
otherwise make the corresponding minimal edits in the existing stack. Never
start another PocketBase instance against the same live data directory.

After deployment, verify the running image digest, application health and
ordinary signed-in Surmai behavior. Check unauthenticated MCP discovery and
rejection, then complete a real OAuth connection as the mapped normal user;
verify permitted reads, scoped writes, denial for missing scope and unmapped
identities, and that data access follows that user's existing permissions.
Avoid logging tokens, identity mappings, or trip data. A healthy container or
successful OAuth login alone is not proof that the MCP workflow works.

## Rollback

If checks fail, restore the prior stack settings and exact prior image reference
and redeploy the existing service against the same data mount. Verify the image,
normal application access and data. Do not remove volumes. Image rollback does
not undo database migrations or writes: assess compatibility before rollout;
restore the verified backup only if necessary and after accounting for data
created since it was taken. Keep the backup and prior image until verification
is complete.
