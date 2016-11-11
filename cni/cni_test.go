package main_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var _ = Describe("Neutron CNI Plugin", func() {

	var (
		neutronServer *httptest.Server
		stateDir      string
		cmd           *exec.Cmd
		cniArgs       string
		input         string
		networkID     string
		authToken     string
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
  "stateDir": "%s",
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
                "ip_address": "1.2.3.4",
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

	var cniCommand = func(command, input, args string) *exec.Cmd {
		toReturn := exec.Command(paths.PathToPlugin)
		toReturn.Env = []string{
			"CNI_COMMAND=" + command,
			"CNI_CONTAINERID=some-container-id",
			"CNI_NETNS=/some/netns/path",
			"CNI_IFNAME=some-eth0",
			"CNI_PATH=" + paths.CNIPath,
			"CNI_ARGS=" + args,
		}
		toReturn.Stdin = strings.NewReader(input)
		return toReturn
	}

	BeforeEach(func() {
		var err error
		// setup fake neutron server
		neutronServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				w.WriteHeader(http.StatusCreated)
				w.Write([]byte(createPortResp))
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			}

		}))

		stateDir, err = ioutil.TempDir("", "cniStateDir")
		Expect(err).ToNot(HaveOccurred())

		authToken = "some-token"
		networkID = "6aeaf34a-c482-4bd3-9dc3-7faf36412f12"

		cniArgs = fmt.Sprintf("AUTH_TOKEN=%s;NETWORK_ID=%s", authToken, networkID)

		input = fmt.Sprintf(inputTemplate, neutronServer.URL, stateDir)
	})

	AfterEach(func() {
		neutronServer.Close()
		os.Remove(stateDir)
	})

	Context("ADD and DEL", func() {
		It("invokes noop delegate", func() {
			By("calling ADD")
			cmd = cniCommand("ADD", input, cniArgs)
			session, err := gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session).Should(gexec.Exit(0))
			Expect(session.Out.Contents()).To(MatchJSON(`{ "ip4": { "ip": "1.2.3.4/32" }, "dns":{} }`))

			By("checking container state info stored")
			path := filepath.Join(stateDir, "some-container-id")
			// TODO: BeARegularFile matcher not working
			_, err = os.Stat(path)
			Expect(err).NotTo(HaveOccurred())
			data, err := ioutil.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(data).To(MatchJSON(`{
  "ip": "1.2.3.4",
   "neutron_port_id": "ebe69f1e-bc26-4db5-bed0-c0afb4afe3db"
}`))

			By("calling DEL")
			cmd = cniCommand("DEL", input, cniArgs)
			session, err = gexec.Start(cmd, GinkgoWriter, GinkgoWriter)
			Expect(err).NotTo(HaveOccurred())
			Eventually(session).Should(gexec.Exit(0))

			By("checking container state info is removed")
			_, err = os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})
})
