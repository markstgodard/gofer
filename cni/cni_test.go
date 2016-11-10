package main_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	noop_debug "github.com/containernetworking/cni/plugins/test/noop/debug"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Neutron CNI Plugin", func() {

	var (
		cmd             *exec.Cmd
		debugFileName   string
		networkID       string
		authToken       string
		debug           *noop_debug.Debug
		expectedCmdArgs skel.CmdArgs

		neutronServer *httptest.Server
	)

	const delegateInput = `
{
		"type": "noop",
		"some": "other data"
}
`

	const inputTemplate = `{
	"cniVersion": "0.2.0",
  "name": "cni-neutron-noop",
  "type": "gofer",
  "neutronURL": "%s",
	"delegate": ` +
		delegateInput +
		`}`

	const createPortResp = `{
    "port": {
        "admin_state_up": true,
        "device_id": "d6b4d3a5-c700-476f-b609-1493dd9dadc0",
        "device_owner": "",
        "fixed_ips": [
            {
                "ip_address": "192.168.111.4",
                "subnet_id": "22b44fc2-4ffb-4de4-b0f9-69d58b37ae27"
            }
        ],
        "id": "ebe69f1e-bc26-4db5-bed0-c0afb4afe3db",
        "mac_address": "fa:16:3e:a6:50:c1",
        "name": "some-container-id",
        "network_id": "6aeaf34a-c482-4bd3-9dc3-7faf36412f12",
        "status": "ACTIVE",
        "tenant_id": "cf1a5775e766426cb1968766d0191908"
    }
}`

	var cniCommand = func(command, input string) *exec.Cmd {

		cniArgs := fmt.Sprintf("DEBUG=%s;AUTH_TOKEN=%s;NETWORK_ID=%s", debugFileName, authToken, networkID)

		toReturn := exec.Command(paths.PathToPlugin)
		toReturn.Env = []string{
			"CNI_COMMAND=" + command,
			"CNI_CONTAINERID=some-container-id",
			"CNI_NETNS=/some/netns/path",
			"CNI_IFNAME=some-eth0",
			"CNI_PATH=" + paths.CNIPath,
			"CNI_ARGS=" + cniArgs,
		}
		toReturn.Stdin = strings.NewReader(input)
		return toReturn
	}

	BeforeEach(func() {
		// setup fake neutron server
		neutronServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(createPortResp))
		}))

		authToken = "some-token"
		networkID = "6aeaf34a-c482-4bd3-9dc3-7faf36412f12"

		debugFile, err := ioutil.TempFile("", "cni_debug")
		Expect(err).NotTo(HaveOccurred())
		Expect(debugFile.Close()).To(Succeed())
		debugFileName = debugFile.Name()

		debug = &noop_debug.Debug{
			ReportResult:         `{ "ip4": { "ip": "1.2.3.4/32" } }`,
			ReportVersionSupport: []string{"0.1.0", "0.2.0", "0.3.0"},
		}
		Expect(debug.WriteDebug(debugFileName)).To(Succeed())

		input := fmt.Sprintf(inputTemplate, neutronServer.URL)

		expectedCmdArgs = skel.CmdArgs{
			ContainerID: "some-container-id",
			Netns:       "/some/netns/path",
			IfName:      "some-eth0",
			Args:        "DEBUG=" + debugFileName,
			Path:        "/some/bin/path",
			StdinData:   []byte(input),
		}
		cmd = cniCommand("ADD", input)
	})

	AfterEach(func() {
		neutronServer.Close()
		os.Remove(debugFileName)
	})

	Context("ADD", func() {
		It("invokes noop delegate", func() {
			session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session).Should(gexec.Exit(0))
			Expect(session.Out.Contents()).To(MatchJSON(`{ "ip4": { "ip": "1.2.3.4/32" }, "dns":{} }`))
		})
	})
})