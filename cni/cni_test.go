package main_test

import (
	"io/ioutil"
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
		debug           *noop_debug.Debug
		expectedCmdArgs skel.CmdArgs
	)

	const delegateInput = `
{
		"type": "noop",
		"some": "other data"
}
`

	const input = `{
	"cniVersion": "0.2.0",
  "name": "cni-neutron-noop",
  "type": "gofer",
	"delegate": ` +
		delegateInput +
		`}`

	var cniCommand = func(command, input string) *exec.Cmd {
		toReturn := exec.Command(paths.PathToPlugin)
		toReturn.Env = []string{
			"CNI_COMMAND=" + command,
			"CNI_CONTAINERID=some-container-id",
			"CNI_NETNS=/some/netns/path",
			"CNI_IFNAME=some-eth0",
			"CNI_PATH=" + paths.CNIPath,
			"CNI_ARGS=DEBUG=" + debugFileName,
		}
		toReturn.Stdin = strings.NewReader(input)
		return toReturn
	}

	BeforeEach(func() {
		debugFile, err := ioutil.TempFile("", "cni_debug")
		Expect(err).NotTo(HaveOccurred())
		Expect(debugFile.Close()).To(Succeed())
		debugFileName = debugFile.Name()

		debug = &noop_debug.Debug{
			ReportResult:         `{ "ip4": { "ip": "1.2.3.4/32" } }`,
			ReportVersionSupport: []string{"0.1.0", "0.2.0", "0.3.0"},
		}
		Expect(debug.WriteDebug(debugFileName)).To(Succeed())

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
