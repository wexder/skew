# skew

`skew` runs local development commands in a Bubble Tea terminal dashboard. The
sidebar lists all configured services. The grid shows the services assigned to
visible panels, with the number of columns chosen automatically to fit the
terminal. Services without a visible panel continue running and collecting
output.

## Configuration

Create a `skew.yaml` in your project directory:

```yaml
services:
  - name: api
    command: go run ./cmd/api
    cwd: .
    env:
      LOG_LEVEL: debug
    watch:
      paths: [cmd/api, internal]
      ignore: ["**/*_test.go"]
      debounce: 250ms

  - name: web
    command: pnpm dev
    cwd: ./web
    watch:
      paths: [src]
      ignore: [node_modules, .next]
```

Commands are shell strings executed with `sh -c`. Relative `cwd` values are
resolved from the directory containing the config file. Configured environment
variables are added to the current environment or replace matching variables.
Each service can set `watch.paths` to files or directories to monitor. Directories
are watched recursively. A matching change restarts that service; stopped
services stay stopped. `watch.ignore` accepts paths or glob patterns relative to
the service's working directory, including `**`. The default debounce is 250ms;
set `watch.debounce` to another positive Go duration such as `500ms` to group
rapid file changes.

## Run

```sh
go run .
go run . -f path/to/skew.yaml
go run . -only api,web
```

All services start automatically unless `-only` selects a comma-separated list.
The initial panels are filled in config order and labeled `A` through `Z`, then
`AA`, `AB`, and so on. To assign a service, focus it in the sidebar and press
`enter`, then press the letter shown on the target panel. Panels `AA` and later
can be selected with the left/right arrows and assigned with Space. With one
visible panel, Enter assigns it immediately. If the service is already visible,
its current panel and the target panel swap assignments.

## Controls

| Key | Action |
| --- | --- |
| `Tab` | Switch between sidebar and panel grid |
| `↑` / `↓`, `k` / `j` | Select a service in the sidebar, or scroll logs in the grid |
| `←` / `→`, `h` / `l` | Focus a panel in the grid |
| `enter` on sidebar | Choose a panel for the selected service |
| `a`–`z` after Enter | Assign to matching panel `A`–`Z` |
| `←` / `→`, then Space after Enter | Choose and assign panels `AA` onward |
| `+` / `-` | Increase or decrease the number of visible panels |
| `0` | Return to the automatic panel count |
| `s` | Start or stop the selected service / focused panel |
| `r` | Restart the selected service / focused panel |
| `c` | Clear the selected service's or focused panel's output |
| `q`, `esc` | Stop all services and quit outside panel choice; `esc` cancels panel choice |
| `ctrl+c` | Stop all services and quit |
