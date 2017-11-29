#!/usr/bin/env bash

#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#

GO_VERSION='go1.8.3'

if go version; then
    EXISTING_GO_VERSION=$(go version | cut -d' ' -f3)
    if [[ "$EXISTING_GO_VERSION" = "$GO_VERSION" ]]; then
        echo "$GO_VERSION already installed"
        exit 0
    fi
fi

GO_DOWNLOADS_URL=https://storage.googleapis.com/golang
GO_TARBALL_VERSION=${GO_VERSION}.linux-amd64.tar.gz
GO_TARBALL_URL=$GO_DOWNLOADS_URL/$GO_TARBALL_VERSION

GO_TARBALL_VERSION_SHA_FILE=$GO_TARBALL_VERSION.sha256
GO_TARBALL_VERSION_SHA_URL=$GO_DOWNLOADS_URL/$GO_TARBALL_VERSION_SHA_FILE
INSTALL_DIR=/usr/local
GOROOT=$INSTALL_DIR/go
GO_BINARY=$GOROOT/bin/go

# Make sure git is installed before proceeding
yum -y install git

rm -rf /usr/local/go

cd /tmp
rm $GO_TARBALL_VERSION
rm $GO_TARBALL_VERSION_SHA_FILE
curl -O $GO_TARBALL_URL
curl -O $GO_TARBALL_VERSION_SHA_URL

echo $GO_TARBALL_VERSION_SHA_FILE
sha256sum -c <(cat $GO_TARBALL_VERSION_SHA_FILE; echo " ./$GO_TARBALL_VERSION")

if [[ $? ]]; then
    cd /usr/local
    echo "Extracting go tarball to $INSTALL_DIR/go"
    tar -zxf /tmp/$GO_TARBALL_VERSION
else
    echo "Checksum failed please verify $GO_TARBALL_VERSION against $GO_TARBALL_VERSION_SHA_FILE"
fi
