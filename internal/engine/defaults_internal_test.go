package engine

import (
	"reflect"
	"testing"
)

func TestParseResolversTakesTheDefaultResolver(t *testing.T) {
	out := `DNS configuration

resolver #1
  nameserver[0] : 127.0.2.2
  nameserver[1] : 10.1.2.3
  flags    : Request A records

resolver #2
  domain   : local
  nameserver[0] : 224.0.0.251

DNS configuration (for scoped queries)

resolver #1
  nameserver[0] : 192.168.1.1
`
	if got := parseResolvers(out); !reflect.DeepEqual(got, []string{"127.0.2.2", "10.1.2.3"}) {
		t.Fatalf("got %v", got)
	}
}

func TestReachableResolversDropsHostOnlyAddresses(t *testing.T) {
	got := reachableResolvers([]string{"127.0.0.1", "::1", "fe80::1", "169.254.1.1", "0.0.0.0", "2001:db8::1", "10.0.0.2", "junk"})
	if !reflect.DeepEqual(got, []string{"10.0.0.2"}) {
		t.Fatalf("got %v", got)
	}
}
