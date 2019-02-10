#!/bin/bash
set -e

# version and keys are supplied as arguments
version="$1"
rc=`echo $version | awk -F - '{print $2}'`
keyfile="$2"
pubkeyfile="$3" # optional
if [[ -z $version || -z $keyfile ]]; then
	echo "Usage: $0 VERSION KEYFILE"
	exit 1
fi
if [[ -z $pubkeyfile ]]; then
	echo "Warning: no public keyfile supplied. Binaries will not be verified."
fi

# check for keyfile before proceeding
if [ ! -f $keyfile ]; then
    echo "Key file not found: $keyfile"
    exit 1
fi
keysum=$(shasum -a 256 $keyfile | cut -c -64)
if [ $keysum != "92269cd84af7dabdf3aa358ff3d154ad68bcfd837e29d32356eda13eb9771089" ]; then
    echo "Wrong key file: checksum does not match developer key file."
    exit 1
fi

# setup build-time vars
ldflags="-s -w -X 'gitlab.com/SiaPrime/SiaPrime/build.GitRevision=`git rev-parse --short HEAD`' -X 'gitlab.com/SiaPrime/SiaPrime/build.BuildTime=`date`' -X 'gitlab.com/SiaPrime/SiaPrime/build.ReleaseTag=${rc}'"

for os in darwin linux windows; do
	echo Packaging ${os}...
	# create workspace
	folder=release/SiaPrime-$version-$os-amd64
	rm -rf $folder
	mkdir -p $folder
	# compile and sign binaries
	for pkg in spc spd; do
		bin=$pkg
		if [ "$os" == "windows" ]; then
			bin=${pkg}.exe
		fi
		GOOS=${os} go build -a -tags 'netgo' -ldflags="$ldflags" -o $folder/$bin ./cmd/$pkg
		openssl dgst -sha256 -sign $keyfile -out $folder/${bin}.sig $folder/$bin
		# verify signature
		if [[ -n $pubkeyfile ]]; then
			openssl dgst -sha256 -verify $pubkeyfile -signature $folder/${bin}.sig $folder/$bin
		fi

	done
	# add other artifacts
	cp -r doc LICENSE README.md $folder
	# zip
	(
		cd release
		zip -rq SiaPrime-$version-$os-amd64.zip SiaPrime-$version-$os-amd64
		openssl dgst -sha256 -sign $keyfile -out SiaPrime-$version-$os-amd64.zip.sig SiaPrime-$version-$os-amd64.zip
		# verify signature
		if [[ -n $pubkeyfile ]]; then
			openssl dgst -sha256 -verify $pubkeyfile -signature SiaPrime-$version-$os-amd64.zip.sig SiaPrime-$version-$os-amd64.zip
		fi
	)
done
