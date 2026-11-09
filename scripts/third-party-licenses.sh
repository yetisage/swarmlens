#!/bin/sh
# Writes the licenses of everything that ends up in the swarmlens binary to the
# file named by $1 (default THIRD_PARTY_LICENSES.txt): the modules it is built
# from, and Go's standard library. Binary releases carry it, because the MIT,
# BSD and Apache licenses of those modules ask for their notices to travel with
# the binary. The modules differ by operating system (the Windows build links
# four that the others do not), so this takes those of every system that
# .goreleaser.yaml builds for. A module that ships no license file but says
# which license it is under in its README (go-localereader does) is included
# with that README from its license heading on, and named on stderr; a module
# with neither stops the release.
set -eu

out=${1:-THIRD_PARTY_LICENSES.txt}
rule='================================================================================'

{
	echo "swarmlens is built from the modules below and from Go's standard library."
	echo "Their licenses follow."
	echo

	for goos in linux darwin windows freebsd; do
		GOOS=$goos CGO_ENABLED=0 go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./...
	done |
		sort -u |
		while read -r path version dir; do
			echo "$rule"
			echo "$path $version"
			echo "$rule"
			found=0
			for f in "$dir"/LICENSE* "$dir"/LICENCE* "$dir"/COPYING* "$dir"/NOTICE*; do
				[ -f "$f" ] || continue
				found=1
				echo "--- $(basename "$f")"
				cat "$f"
				echo
			done
			if [ "$found" = 0 ]; then
				readme=$(ls "$dir"/README* 2>/dev/null | head -n 1 || true)
				if [ -n "$readme" ] && grep -qi '^#* *licen[sc]e' "$readme"; then
					echo "--- no license file in the module; its $(basename "$readme") says:"
					sed -n '/^#* *[Ll][Ii][Cc][Ee][Nn][SsCc][Ee]/,$p' "$readme"
					echo
					echo "$path ships no license file; its README names the license" >&2
					found=1
				fi
			fi
			[ "$found" = 1 ] || { echo "$path: no license file found in the module" >&2; exit 1; }
		done

	echo "$rule"
	echo "Go standard library"
	echo "$rule"
	cat "$(go env GOROOT)/LICENSE"
} >"$out"
