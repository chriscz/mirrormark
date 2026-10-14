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

## Development

Everything builds and tests in a Docker image; nothing is installed on your
machine except Docker.

    bin/dev run make          # format check, vet, unit tests, build
    bin/dev shell             # a shell in the dev container

The binary lands in `build/mirrormark`.

## Licence

MPL-2.0. See `LICENSE`.
