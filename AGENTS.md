# Playfielder

This repository is an independent Go authoring application. The existing hosted
Playfielder and its source directories are read-only references for this work.

- Never access production data, model credentials or deployment configuration.
- Repository visibility, license, README and deployments require the owner's
  explicit approval. Do not publish until these choices have been confirmed.
- Keep server and domain logic in Go; browser documents use HTML/CSS/JavaScript.
- Never execute authored or generated JavaScript in the server process. Use
  mature parsers, and sandbox browser previews with opaque origins.
- All durable mutations belong to internal/store and use bbolt transactions.
- Generation lifecycle, cancellation, budget and recovery belong to internal/agent.
- Check revision conflicts, package limits, origins and data ownership at boundaries.
- Keep examples synthetic. Do not publish original games, personal files or secrets.
- Test crash recovery, stale writes, unsafe archives and real browser interactions.
