---
name: surmai-itinerary
description: Prepare reviewable changes to Surmai activities, lodging, or transportation, and check their approval status while preserving existing trip information.
---

Use `list_trips` and `get_trip_itinerary` to establish current trip and record IDs before proposing changes. Preserve unrelated fields. Ask one concise question if required details are missing; never invent reservation codes, addresses, or flight times.

Call `propose_itinerary_changes` with one coherent batch of 1 to 25 changes. The tool creates a proposal, not itinerary records. It requires `surmai:write` in addition to the connection's read permission. If write access is missing, explain that the connection must be reauthorized; do not ask for credentials in chat.

Each change needs `operation` (create, update, or delete) and `entity_type` (activity, lodging, or transportation). Creates omit `record_id`; updates and deletes use IDs returned by Surmai. Combine edits to the same record into one change. Only propose deletes explicitly requested by the user.

Supported fields:

| Entity | Fields |
| --- | --- |
| Activity | name, description, address, start_time, end_time, link, metadata |
| Lodging | name, type, address, start_time, end_time, confirmation, link, metadata |
| Transportation | type, origin, destination, departure_time, arrival_time, notes, link, metadata |

An activity create requires name and start_time. A lodging create requires name, type, address, start_time, and end_time. A transportation create requires type, origin, destination, departure_time, and arrival_time. Use supported Surmai types observed in existing records, and RFC3339 times with the event's local timezone offset. The backend preserves local wall-clock time for Surmai storage.

Use `clear` only for explicitly requested supported scalar fields; null field values preserve existing values. Metadata must be a nonempty object and replaces the complete metadata object, so merge existing metadata before changing it. Do not put trip IDs, ownership, expenses, participants, or attachment references inside fields. Cost edits and attachment uploads are not supported.

After creation, show the returned preview and clickable `review_url`. Tell the user to approve or reject it in the trip's Assistant tab in Surmai. Never say a proposal was applied while pending. Use `get_proposal` to verify approval status when asked. There is no MCP apply tool and no reason to invoke another AI assistant for this operation.

If the trip or a target changed after preview, approval rejects the stale proposal. Read the trip again and create a fresh proposal that reflects the user's requested result. Do not retry stale IDs or silently overwrite changes.
