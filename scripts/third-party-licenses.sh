#!/bin/sh
# Writes the licenses of everything that ends up in the swarmlens binary to the
# file named by $1 (default THIRD_PARTY_LICENSES.txt): the modules it is built
# from, and Go's standard library. Binary releases carry it, because the MIT,
# BSD and Apache licenses of those modules ask for their notices to travel with
# the binary.
set -eu

out=${1:-THIRD_PARTY_LICENSES.txt}
rule='================================================================================'

{
	echo "swarmlens is built from the modules below and from Go's standard library."
	echo "Their licenses follow."
	echo

	go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}' ./... |
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
			[ "$found" = 1 ] || { echo "no license file found in the module" >&2; exit 1; }
		done

	echo "$rule"
	echo "Go standard library"
	echo "$rule"
	cat "$(go env GOROOT)/LICENSE"
} >"$out"
