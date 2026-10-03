package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

func TestAssistantGuideFollowsDNSAndJobState(t *testing.T) {
	w := core.NewWorld()
	pc := w.Devices["pc-alex"]
	router := w.Devices["router-alex"]

	beforeMain, beforeAssistant := w.WalletBalance("alex")
	out := run(t, w, pc, "alex", "assist guide")
	for _, want := range []string{"SERVFAIL", "ping ", "dig mirror.neohome.example", "ssh root@"} {
		if !strings.Contains(out, want) {
			t.Fatalf("initial guide should explain the observed failure and next checks; missing %q:\n%s", want, out)
		}
	}
	if w.Job("J-101").Accepted != "" || w.Job("J-101").Done {
		t.Fatal("guide must not accept or complete work for the player")
	}
	afterMain, afterAssistant := w.WalletBalance("alex")
	if beforeMain != afterMain || beforeAssistant != afterAssistant {
		t.Fatalf("guide must not change wallet balances: before=(%v,%v) after=(%v,%v)", beforeMain, beforeAssistant, afterMain, afterAssistant)
	}

	data, ok := router.FS.Read("/etc/dnsmasq.conf")
	if !ok {
		t.Fatal("router dnsmasq config is missing")
	}
	router.FS.Write("/etc/dnsmasq.upstream", "nameserver 10.0.0.2\n", 0644, "root", "root")
	fixed := strings.Replace(string(data), "resolv-file=/var/run/dnsmasq/resolv.conf", "resolv-file=/etc/dnsmasq.upstream", 1)
	router.FS.Write("/etc/dnsmasq.conf", fixed, 0644, "root", "root")
	if _, err := router.RestartService("dnsmasq"); err != nil {
		t.Fatalf("restart dnsmasq: %v", err)
	}

	out = run(t, w, pc, "alex", "assist guide")
	if !strings.Contains(out, "DNS repair is verified") || !strings.Contains(out, "job accept J-101") {
		t.Fatalf("guide should switch to the verified job claim steps after the repair:\n%s", out)
	}
	if w.Job("J-101").Accepted != "" || w.Job("J-101").Done {
		t.Fatal("guide must leave job acceptance and payment to the player")
	}

	if out = run(t, w, pc, "alex", "job accept J-101"); !strings.Contains(out, "accepted") {
		t.Fatalf("player should be able to accept the verified job: %s", out)
	}
	out = run(t, w, pc, "alex", "assist guide")
	if !strings.Contains(out, "job pay J-101") {
		t.Fatalf("guide should recognize the accepted job and point to payment:\n%s", out)
	}
	if _, _, err := w.PayJob("alex", "J-101"); err != nil {
		t.Fatalf("pay verified repair job: %v", err)
	}
	out = run(t, w, pc, "alex", "assist guide")
	if !strings.Contains(out, "already completed") || !strings.Contains(out, "job list") {
		t.Fatalf("guide should recognize the completed job and advance to available work:\n%s", out)
	}
}
