#! /bin/bash

set -e -u
set -o pipefail

export CNI_COMMAND=ADD
export CNI_CONTAINERID=some-container-id

NEUTRON_URL="http://192.168.56.101:9696"

STATE_DIR=/tmp/cni
mkdir -p ${STATE_DIR}

mkdir -p ${PWD}/bin

NET_ID="ad9845a2-64fe-4127-ab1b-7aa342a2b554"
TOKEN=`./scripts/token.sh`

export CNI_ARGS="AUTH_TOKEN=${TOKEN};NETWORK_ID=${NET_ID}"
export CNI_NETNS=/some/netns/path
export CNI_IFNAME=some-eth0
export CNI_PATH=${PWD}/bin

pushd cni
go build -o ${CNI_PATH}/gofer
popd
pushd cni/noop
go build -o ${CNI_PATH}/ovs
popd

INPUT_WRAPPER=$(cat <<END
{
  "name": "cni-neutron-ovs",
  "type": "gofer",
  "cniVersion": "0.2.0",
  "neutronURL": "${NEUTRON_URL}",
  "stateDir": "${STATE_DIR}",
  "delegate": {
    "name": "ovs",
    "type": "ovs",
    "bridge": "br-int"
  },
  "metadata": {
    "app_id": "app-guid-1",
    "space_id": "space-guid-1",
    "policy_group_id": "app-guid-1"
  }
}
END
)
echo $CNI_ARGS
echo  $INPUT_WRAPPER | jq .
echo  $INPUT_WRAPPER | ${CNI_PATH}/gofer
