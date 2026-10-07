package tests

import (
	"strings"
	"testing"

	"neohome/internal/core"
)

// The phone is a real device: battery hardware, a terminal service, a
// contact list, and a message spool that already holds its seeded traffic.
func TestPhoneIsARealDevice(t *testing.T) {
	w := core.NewWorld()
	phone := w.Devices["phone-alex"]
	if phone == nil || phone.Profile != "phone" {
		t.Fatal("no phone in the household")
	}
	if !phone.HW.Battery {
		t.Fatal("a phone without battery hardware is a lie")
	}
	svc := phone.Svc("sshd")
	if svc == nil || svc.State != "running" {
		t.Fatal("the phone's terminal is not reachable")
	}
	if contacts, ok := phone.FS.Read("/home/alex/.contacts"); !ok || !strings.Contains(string(contacts), "mara 555-0001") {
		t.Fatalf("the contact list is not a real file:\n%s", contacts)
	}
	msgs, err := w.SMSInbox(phone, phone.FindUser("alex"))
	if err != nil || len(msgs) != 2 {
		t.Fatalf("the seeded conversation is missing: %d msgs err=%v", len(msgs), err)
	}
	if msgs[0].From != "bank" || !strings.Contains(msgs[1].Text, "resolv-file") {
		t.Fatalf("the seeded messages are not the world's real story: %+v", msgs)
	}
	// mara carries one too, on the other side of town
	if w.Devices["phone-mara"] == nil {
		t.Fatal("mara has no phone")
	}
}

func TestSMSSendAndNPCReply(t *testing.T) {
	w := core.NewWorld()
	phone := w.Devices["phone-alex"]
	mara := w.Devices["phone-mara"]
	alex := phone.FindUser("alex")

	// a message really lands in mara's spool, and she really answers
	if err := w.SMSSend(phone, alex, "mara", "hey, is my wifi the usual story?"); err != nil {
		t.Fatalf("send failed: %v", err)
	}
	maraMsgs, _ := w.SMSInbox(mara, mara.FindUser("mara"))
	if len(maraMsgs) == 0 || maraMsgs[len(maraMsgs)-1].From != "alex" {
		t.Fatalf("the message did not reach mara's spool: %+v", maraMsgs)
	}
	msgs, _ := w.SMSInbox(phone, alex)
	last := msgs[len(msgs)-1]
	if last.From != "mara" || !strings.Contains(last.Text, "resolv") {
		t.Fatalf("mara did not answer in character: %+v", last)
	}

	// the message files are real files on the phone, owned by the owner
	node, ok := phone.FS.Get("/var/spool/sms/0003.txt")
	if !ok || node.Owner != "alex" {
		t.Fatal("the reply is not a real file in the spool")
	}

	// unknown contact and unknown number are honest failures
	if err := w.SMSSend(phone, alex, "nobody", "hi"); err == nil {
		t.Fatal("an unknown contact was accepted")
	}
	if err := w.SMSSend(phone, alex, "555-9999", "hi"); err == nil {
		t.Fatal("an unknown number was accepted")
	}
}

// THE phone property: SMS rides the cellular radio, not your Wi-Fi. Take
// the whole home network down and the conversation keeps going.
func TestSMSCellularIsOutOfBand(t *testing.T) {
	w := core.NewWorld()
	phone := w.Devices["phone-alex"]
	alex := phone.FindUser("alex")

	w.Devices["router-alex"].MainsDropped = true
	// the internet is gone — DNS fails, everything else does too...
	_, ok, _ := core.DNSAnswer(phone, "mirror.neohome.example")
	if ok {
		t.Fatal("setup: the network should be down")
	}
	// ...but the phone still talks
	if err := w.SMSSend(phone, alex, "mara", "power cut here, network is dead"); err != nil {
		t.Fatalf("SMS must survive a dead router: %v", err)
	}
	msgs, _ := w.SMSInbox(phone, alex)
	if msgs[len(msgs)-1].From != "mara" {
		t.Fatal("no reply over the cellular radio")
	}
}

// Banks text you when money arrives.
func TestBankDepositSMS(t *testing.T) {
	w := core.NewWorld()
	repairDNS(t, w)
	phone := w.Devices["phone-alex"]
	alex := phone.FindUser("alex")
	before, _ := w.SMSInbox(phone, alex)

	if out := run(t, w, w.Devices["pc-alex"], "alex", "job accept J-101"); !strings.Contains(out, "accepted") {
		t.Fatalf("job accept failed:\n%s", out)
	}
	if _, _, err := w.PayJob("alex", "J-101"); err != nil {
		t.Fatalf("pay failed: %v", err)
	}
	msgs, _ := w.SMSInbox(phone, alex)
	if len(msgs) != len(before)+1 {
		t.Fatalf("the deposit did not produce exactly one SMS: %d -> %d", len(before), len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.From != "bank" || !strings.Contains(last.Text, "J-101") || !strings.Contains(last.Text, "balance") {
		t.Fatalf("the receipt is not the real deposit: %+v", last)
	}
	// an account without a phone on file gets nothing, and nothing breaks
	w.BankSMS("sysmods", "hello")
}

func TestPhoneBatteryLifecycle(t *testing.T) {
	w := core.NewWorld()
	phone := w.Devices["phone-alex"]
	pc := w.Devices["pc-alex"]
	alex := phone.FindUser("alex")

	w.SMS.Battery[phone.ID] = 1
	w.TickCount = 479
	w.Tick() // crosses a 480-tick boundary: the last percent is gone
	if w.SMS.Battery[phone.ID] != 0 {
		t.Fatalf("battery did not drain: %d", w.SMS.Battery[phone.ID])
	}
	if phone.Svc("sshd").State != "stopped" {
		t.Fatal("a dead phone must really power off its terminal")
	}
	// the whole machine agrees it is off: no shell, and the reason names
	// the battery instead of a generic failure
	if phone.Powered() {
		t.Fatal("a phone at 0% must not be powered")
	}
	if got := phone.UnavailableReason(); got != "battery empty" {
		t.Fatalf("the reason must name the battery, got %q", got)
	}
	if out := run(t, w, phone, "alex", "uptime"); !strings.Contains(out, "battery empty") {
		t.Fatalf("no command may run on a dark phone, got:\n%s", out)
	}
	if err := w.SMSSend(phone, alex, "mara", "anyone there?"); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("a dead phone must not receive: %v", err)
	}
	// the physical act, from another machine: docking is hands, not a shell
	// command, so a dead phone is never a one-way door
	out := run(t, w, pc, "alex", "phone charge")
	if !strings.Contains(out, "battery 100%") {
		t.Fatalf("charging from the PC failed:\n%s", out)
	}
	if !phone.Powered() {
		t.Fatal("the phone must be back after charging")
	}
	if phone.Svc("sshd").State != "running" {
		t.Fatal("the terminal did not come back after charging")
	}
	if out := run(t, w, phone, "alex", "phone status"); !strings.Contains(out, "100%") {
		t.Fatalf("status should show the full battery:\n%s", out)
	}
}
