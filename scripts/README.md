# scripts/

The agent's own scripts — Pi favours plain scripts over MCP. Baked into the
image at build, bind-mounted live over that at runtime, and on the container's
`PATH` (`/home/pi/scripts`).

Gitignored except this README: scripts tend to encode personal specifics
(internal hosts, team conventions). Write your own here; they persist across
restarts.
