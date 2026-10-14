# mirrormark

mirrormark keeps chosen bookmark folders the same across browser profiles on
one computer. It is a browser extension plus one Go program, and it stores
everything in YAML files you can read. No server, no account, no network.

Status: in development. Google Chrome on Linux comes first.

## Files

| File | What it is |
| --- | --- |
| `~/.config/mirrormark/config.yaml` | Your unions. You write it. |
| `~/.local/share/mirrormark/profiles/<id>.yaml` | One database per bound profile. You may edit its `profile:` block; mirrormark writes the rest. |
| `~/.local/share/mirrormark/backups/<id>/` | One copy of each database a day, the newest 30. For reading only. |
| `~/.local/state/mirrormark/log` | The host's log. |

`$XDG_CONFIG_HOME`, `$XDG_DATA_HOME` and `$XDG_STATE_HOME` move these when set.
Database and backup files are readable only by you (mode 0600); they hold
your bookmarks in plain text.

### config.yaml

    format: 1
    unions:
      work:                            # any name
        chrome-personal: toolbar/Work  # profile id: folder path
        chrome-work: toolbar/Work
      reading:
        chrome-personal: other/Reading
        chrome-work: other/Reading

A path starts with `toolbar`, `other` or `mobile`, or with the title Chrome
shows for that folder, such as `Bookmarks bar`. Write `\/` for a slash inside
a folder name. One profile's folders may not be inside each other.

## How syncing behaves

- A union holds everything any member holds, at the same relative paths.
- Deleting a bookmark in a union moves it to a `_deleted` folder inside the
  union, in every profile. Restore it by moving it out. Delete it from
  `_deleted` to remove it everywhere.
- A round that would delete or purge more than half of a union (of more than
  five bookmarks and folders) syncs nothing for that union, because a browser
  that has not loaded its bookmarks or a damaged file is far likelier. Empty a
  large `_deleted` in parts.
- When two profiles change the same bookmark differently, you get both,
  titled for example `GitHub (chrome/personal)` and `GitHub (chrome/work)`.
  For folders, the change that reached the files first wins. Deleting a
  bookmark that another profile moved meanwhile counts as such a change: the
  moved copy stays where it went, and yours lands in `_deleted`.
- Reordering bookmarks inside a folder stays local.
- A missing member folder is created on the first round. If it later
  disappears or is replaced by another folder at that path, that profile
  stops syncing the union until you restore the folder or change its path in
  `config.yaml`; a new path makes the profile a new member, which deletes
  nothing.
- While any member's database file cannot be read, the union does not sync.

## Development

Everything builds and tests in a Docker image; nothing is installed on your
machine except Docker.

    bin/dev run make          # format check, vet, unit tests, build
    bin/dev shell             # a shell in the dev container

The union fuzz tests in `internal/merge` and `internal/round` run 300 seeds;
run more with, for example,
`bin/dev run go test ./internal/round -args -mirrormark.fuzz=20000`.

The binary lands in `build/mirrormark`.

## Licence

MPL-2.0. See `LICENSE`.
