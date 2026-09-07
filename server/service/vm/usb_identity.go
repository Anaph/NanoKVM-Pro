package vm

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// The attached host identifies the emulated keyboard, mouse and touchpad by the
// descriptors of the composite gadget that carries them, so changing the gadget
// identity is what renames the HID devices on the host side.
//
// usbdev.sh rebuilds the gadget on every boot and reads each of these files
// when it does, falling back to the built-in default when one is absent. Writing
// them is therefore the supported way to override the descriptor: the value
// survives reboots without any help from the server, and clearing a file
// restores the factory value.
const (
	gadgetDir   = "/sys/kernel/config/usb_gadget/g0"
	usbScript   = "/kvmapp/scripts/usbdev.sh"
	bootConfDir = "/boot"

	// USB string descriptors carry UTF-16 code units in a 255-byte packet,
	// which leaves room for 126 characters.
	maxUSBStringLen = 126

	// PresetCustom marks an identity that matches none of the built-in presets.
	PresetCustom = "custom"
	// PresetFactory clears the overrides and lets usbdev.sh use its defaults.
	PresetFactory = "factory"
)

var usbIdentityMu sync.Mutex

// UsbIdentity is the subset of the gadget descriptor that the host displays.
type UsbIdentity struct {
	VendorID     string `json:"vendorId"`
	ProductID    string `json:"productId"`
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
	Serial       string `json:"serial"`
}

// usbIdentityFiles maps each field to the /boot file usbdev.sh reads it from.
var usbIdentityFiles = []struct {
	file string
	get  func(*UsbIdentity) string
	set  func(*UsbIdentity, string)
}{
	{"usb.vid", func(i *UsbIdentity) string { return i.VendorID }, func(i *UsbIdentity, v string) { i.VendorID = v }},
	{"usb.pid", func(i *UsbIdentity) string { return i.ProductID }, func(i *UsbIdentity, v string) { i.ProductID = v }},
	{"usb.manufacturer", func(i *UsbIdentity) string { return i.Manufacturer }, func(i *UsbIdentity, v string) { i.Manufacturer = v }},
	{"usb.product", func(i *UsbIdentity) string { return i.Product }, func(i *UsbIdentity, v string) { i.Product = v }},
	{"usb.serialnumber", func(i *UsbIdentity) string { return i.Serial }, func(i *UsbIdentity, v string) { i.Serial = v }},
}

type usbPreset struct {
	Name        string
	Description string
	Identity    UsbIdentity
}

// factoryIdentity mirrors the defaults hardcoded in usbdev.sh's hid_start. It is
// only used to label the current descriptor in the UI; selecting the factory
// preset removes the override files rather than writing these values back.
var factoryIdentity = UsbIdentity{
	VendorID:     "0x3346",
	ProductID:    "0x1009",
	Manufacturer: "sipeed",
	Product:      "NanoKVMPro",
	Serial:       "0123456789ABCDEF",
}

// Built-in presets. The factory entry is handled separately, by clearing the
// override files, so it is not listed here.
var usbPresets = []usbPreset{
	{
		Name:        "logitech-classic",
		Description: "Logitech Cordless Desktop receiver (keyboard + mouse)",
		Identity: UsbIdentity{
			VendorID:     "0x046d",
			ProductID:    "0xc517",
			Manufacturer: "Logitech",
			Product:      "USB Receiver",
			// The real receivers ship no serial number string.
			Serial: "",
		},
	},
}

func (s *Service) GetUsbIdentity(c *gin.Context) {
	var rsp proto.Response

	usbIdentityMu.Lock()
	defer usbIdentityMu.Unlock()

	// The live gadget is the truth; the override files only say which parts of
	// it were chosen rather than defaulted.
	current, err := readGadgetIdentity()
	if err != nil {
		log.Errorf("failed to read usb gadget identity: %s", err)

		// Without a gadget, report what the next rebuild would produce.
		current = pendingIdentity()
	}

	rsp.OkRspWithData(c, buildUsbIdentityRsp(current))
	log.Debugf("get usb identity: %s:%s %q", current.VendorID, current.ProductID, current.Product)
}

func (s *Service) SetUsbIdentity(c *gin.Context) {
	var req proto.SetUsbIdentityReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	usbIdentityMu.Lock()
	defer usbIdentityMu.Unlock()

	if req.Preset == PresetFactory {
		if err := clearIdentityOverrides(); err != nil {
			log.Errorf("failed to clear usb identity overrides: %s", err)
			rsp.ErrRsp(c, -3, "failed to save usb identity")
			return
		}
	} else {
		identity, err := resolveRequestedIdentity(&req)
		if err != nil {
			rsp.ErrRsp(c, -2, err.Error())
			return
		}
		if err := writeIdentityOverrides(identity); err != nil {
			log.Errorf("failed to persist usb identity: %s", err)
			rsp.ErrRsp(c, -3, "failed to save usb identity")
			return
		}
	}

	if err := restartGadget(); err != nil {
		log.Errorf("failed to restart usb gadget: %s", err)
		rsp.ErrRsp(c, -4, "failed to apply usb identity")
		return
	}

	current, err := readGadgetIdentity()
	if err != nil {
		log.Errorf("failed to read usb gadget identity after restart: %s", err)
		current = pendingIdentity()
	}

	rsp.OkRspWithData(c, buildUsbIdentityRsp(current))
	log.Debugf("set usb identity: %s:%s %q", current.VendorID, current.ProductID, current.Product)
}

func buildUsbIdentityRsp(identity *UsbIdentity) *proto.GetUsbIdentityRsp {
	return &proto.GetUsbIdentityRsp{
		VendorID:     identity.VendorID,
		ProductID:    identity.ProductID,
		Manufacturer: identity.Manufacturer,
		Product:      identity.Product,
		Serial:       identity.Serial,
		Preset:       matchPreset(*identity),
		Presets:      listPresets(),
	}
}

func resolveRequestedIdentity(req *proto.SetUsbIdentityReq) (*UsbIdentity, error) {
	if req.Preset != "" && req.Preset != PresetCustom {
		return lookupPreset(req.Preset)
	}

	identity := UsbIdentity{
		VendorID:     strings.TrimSpace(req.VendorID),
		ProductID:    strings.TrimSpace(req.ProductID),
		Manufacturer: strings.TrimSpace(req.Manufacturer),
		Product:      strings.TrimSpace(req.Product),
		Serial:       strings.TrimSpace(req.Serial),
	}

	if err := validateIdentity(&identity); err != nil {
		return nil, err
	}

	return &identity, nil
}

func lookupPreset(name string) (*UsbIdentity, error) {
	for _, preset := range usbPresets {
		if preset.Name == name {
			identity := preset.Identity
			return &identity, nil
		}
	}

	return nil, fmt.Errorf("unknown preset: %s", name)
}

func listPresets() []proto.UsbIdentityPreset {
	presets := []proto.UsbIdentityPreset{
		{
			Name:        PresetFactory,
			Description: "Restore the original NanoKVM Pro descriptor",
		},
	}

	for _, preset := range usbPresets {
		presets = append(presets, proto.UsbIdentityPreset{
			Name:        preset.Name,
			Description: preset.Description,
		})
	}

	return presets
}

func matchPreset(identity UsbIdentity) string {
	if identity == factoryIdentity {
		return PresetFactory
	}

	for _, preset := range usbPresets {
		if identity == preset.Identity {
			return preset.Name
		}
	}

	return PresetCustom
}

func validateIdentity(identity *UsbIdentity) error {
	vendor, err := normalizeUSBID(identity.VendorID)
	if err != nil {
		return fmt.Errorf("invalid vendor id: %w", err)
	}
	identity.VendorID = vendor

	product, err := normalizeUSBID(identity.ProductID)
	if err != nil {
		return fmt.Errorf("invalid product id: %w", err)
	}
	identity.ProductID = product

	for name, value := range map[string]string{
		"manufacturer": identity.Manufacturer,
		"product":      identity.Product,
		"serial":       identity.Serial,
	} {
		if err := validateUSBString(value); err != nil {
			return fmt.Errorf("invalid %s: %w", name, err)
		}
	}

	return nil
}

// normalizeUSBID accepts "0x046d", "046d" or "046D" and returns the canonical
// lowercase 0x-prefixed form that configfs expects.
func normalizeUSBID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("value is empty")
	}

	digits := strings.TrimPrefix(strings.TrimPrefix(trimmed, "0x"), "0X")
	if len(digits) == 0 || len(digits) > 4 {
		return "", errors.New("expected 1 to 4 hexadecimal digits")
	}

	parsed, err := strconv.ParseUint(digits, 16, 16)
	if err != nil {
		return "", errors.New("expected a hexadecimal value")
	}

	return fmt.Sprintf("0x%04x", parsed), nil
}

func validateUSBString(value string) error {
	if len([]rune(value)) > maxUSBStringLen {
		return fmt.Errorf("longer than %d characters", maxUSBStringLen)
	}

	for _, r := range value {
		// usbdev.sh cats each file straight into a configfs attribute, which
		// holds a single line, so a newline would truncate the value.
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return errors.New("contains control characters")
		}
	}

	return nil
}

// pendingIdentity reports the descriptor the next gadget rebuild would produce:
// the override files where present, the usbdev.sh defaults everywhere else.
func pendingIdentity() *UsbIdentity {
	identity := factoryIdentity

	for _, field := range usbIdentityFiles {
		data, err := os.ReadFile(filepath.Join(bootConfDir, field.file))
		if err != nil {
			continue
		}
		field.set(&identity, strings.TrimSpace(string(data)))
	}

	return &identity
}

func writeIdentityOverrides(identity *UsbIdentity) error {
	for _, field := range usbIdentityFiles {
		path := filepath.Join(bootConfDir, field.file)
		// The trailing newline is what makes an empty value work: usbdev.sh
		// cats the file into configfs, and a zero-byte write is rejected.
		if err := os.WriteFile(path, []byte(field.get(identity)+"\n"), 0o644); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
	}

	return nil
}

func clearIdentityOverrides() error {
	for _, field := range usbIdentityFiles {
		path := filepath.Join(bootConfDir, field.file)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
	}

	return nil
}

// restartGadget tears the gadget down and builds it again, which is when
// usbdev.sh re-reads the override files. The host sees this as a re-plug of the
// composite device, so the HID nodes are closed for the duration.
func restartGadget() error {
	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	if err := exec.Command("bash", usbScript, "restart").Run(); err != nil {
		return fmt.Errorf("%s restart failed: %w", usbScript, err)
	}

	// The HID nodes reappear asynchronously after the gadget is rebuilt.
	time.Sleep(3 * time.Second)

	return nil
}

func readGadgetIdentity() (*UsbIdentity, error) {
	vendor, err := readGadgetAttr("idVendor")
	if err != nil {
		return nil, err
	}
	product, err := readGadgetAttr("idProduct")
	if err != nil {
		return nil, err
	}

	normalizedVendor, err := normalizeUSBID(vendor)
	if err != nil {
		return nil, fmt.Errorf("gadget reports invalid vendor id %q: %w", vendor, err)
	}
	normalizedProduct, err := normalizeUSBID(product)
	if err != nil {
		return nil, fmt.Errorf("gadget reports invalid product id %q: %w", product, err)
	}

	identity := &UsbIdentity{
		VendorID:  normalizedVendor,
		ProductID: normalizedProduct,
	}

	// String descriptors are optional; a gadget may ship without them.
	identity.Manufacturer, _ = readGadgetAttr("strings/0x409/manufacturer")
	identity.Product, _ = readGadgetAttr("strings/0x409/product")
	identity.Serial, _ = readGadgetAttr("strings/0x409/serialnumber")

	return identity, nil
}

func readGadgetAttr(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(gadgetDir, name))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
