package main

import (
	"testing"

	"github.com/marrasen/marraw/internal/marrawclient"
)

func TestShootTreeArrangesShootsByTheirFolders(t *testing.T) {
	shoots := []marrawclient.Shoot{
		{Name: "lib", IsSelf: true, PhotoCount: 1},
		{Name: "Concert", Group: "2019", PhotoCount: 2, EarliestTakenAt: 200},
		{Name: "Wedding", Group: "2019", PhotoCount: 3, EarliestTakenAt: 100},
		{Name: "Studio", Group: "Clients/Volvo", PhotoCount: 4, EarliestTakenAt: 900},
		{Name: "Launch", Group: "Clients/Acme", PhotoCount: 5, EarliestTakenAt: 500},
		{Name: "Mixed", PhotoCount: 1, Split: true, EarliestTakenAt: 50},
		{Name: "Second", Group: "Mixed", PhotoCount: 6, EarliestTakenAt: 60},
	}
	names := func(n *shootNode) []string {
		var out []string
		for _, k := range n.kids {
			out = append(out, k.name)
		}
		return out
	}
	tree := shootTree(shoots, "nameAsc")
	if got := names(tree); len(got) != 3 || got[0] != "2019" || got[1] != "Clients" || got[2] != "Mixed" {
		t.Fatalf("the top level is %v, want 2019, Clients, Mixed (the library folder's own photos apart)", got)
	}
	year := tree.kids[0]
	if year.shoot != nil || year.count != 5 || year.latest != 200 {
		t.Fatalf("2019 is %+v, want a group of 5 photos, newest 200", year)
	}
	if got := names(year); got[0] != "Concert" || got[1] != "Wedding" {
		t.Fatalf("2019 holds %v by name", got)
	}
	clients := tree.kids[1]
	if got := names(clients); got[0] != "Acme" || got[1] != "Volvo" || clients.count != 9 || clients.kids[0].rel != "Clients/Acme" {
		t.Fatalf("Clients holds %v, %d photos, first at %q", got, clients.count, clients.kids[0].rel)
	}
	mixed := tree.kids[2]
	if mixed.shoot == nil || len(mixed.kids) != 1 || mixed.kids[0].name != "Second" || mixed.count != 7 {
		t.Fatalf("the split shoot Mixed is %+v, want a shoot with Second under it", mixed)
	}

	// Newest first: a group goes by its newest shoot, a level at a time.
	tree = shootTree(shoots, "dateDesc")
	if got := names(tree); got[0] != "Clients" || got[1] != "2019" || got[2] != "Mixed" {
		t.Fatalf("newest first, the top level is %v", got)
	}
	if got := names(tree.kids[1]); got[0] != "Concert" || got[1] != "Wedding" {
		t.Fatalf("newest first, 2019 holds %v", got)
	}
}
