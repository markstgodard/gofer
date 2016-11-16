package main_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestOvs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Ovs Suite")
}
