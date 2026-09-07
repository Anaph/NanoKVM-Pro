package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
// usbdev.sh rebuilds the gadget from its own defaults on every boot, which is
// why the chosen identity is persisted here and re-applied by RestoreUsbIdentity
// when the server starts.
const (
	gadgetDir   = "/sys/kernel/config/usb_gadget/g0"
	gadgetUDC   = gadgetDir + "/UDC"
	udcClassDir = "/sys/class/udc"

	usbIdentityFile = "/etc/kvm/usb-identity.json"
	// Snapshot of whatever the gadget looked like before the first override,
	// so the factory preset can restore it without hardcoding the defaults.
	usbFactoryFile = "/etc/kvm/usb-identity.factory.json"

	// USB string descriptors carry UTF-16 code units in a 255-byte packet,
	// which leaves room for 126 characters.
	maxUSBStringLen = 126

	// PresetCustom marks an identity that matches none of the built-in presets.
	PresetCustom  = "custom"
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

type usbPreset struct {
	Name        string
	Description string
	Identity    UsbIdentity
}

// Built-in presets. The factory entry is resolved at runtime from the snapshot
// taken before the first override, so it is not listed here.
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

// sipeedIdentity is the fallback for the factory preset on devices that were
// never snapshotted (for example when the config directory was wiped).
var sipeedIdentity = UsbIdentity{
	VendorID:     "0x3346",
	ProductID:    "0x1009",
	Manufacturer: "sipeed",
	Product:      "NanoKVM",
	Serial:       "0123456789ABCDEF",
}

func (s *Service) GetUsbIdentity(c *gin.Context) {
	var rsp proto.Response

	usbIdentityMu.Lock()
	defer usbIdentityMu.Unlock()

	current, err := readGadgetIdentity()
	if err != nil {
		// Without a gadget there is nothing to report, but the stored choice is
		// still worth returning so the UI does not lose the user's selection.
		log.Errorf("failed to read usb gadget identity: %s", err)

		stored, storedErr := loadStoredIdentity()
		if storedErr != nil {
			rsp.ErrRsp(c, -1, "failed to read usb identity")
			return
		}
		current = stored
	}

	rsp.OkRspWithData(c, &proto.GetUsbIdentityRsp{
		VendorID:     current.VendorID,
		ProductID:    current.ProductID,
		Manufacturer: current.Manufacturer,
		Product:      current.Product,
		Serial:       current.Serial,
		Preset:       matchPreset(*current),
		Presets:      listPresets(),
	})

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

	identity, err := resolveRequestedIdentity(&req)
	if err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}

	// Capture the untouched gadget once, so "factory" stays meaningful even
	// after several overrides.
	if err := snapshotFactoryIdentity(); err != nil {
		log.Errorf("failed to snapshot factory usb identity: %s", err)
	}

	if err := saveStoredIdentity(identity); err != nil {
		log.Errorf("failed to persist usb identity: %s", err)
		rsp.ErrRsp(c, -3, "failed to save usb identity")
		return
	}

	if err := applyIdentity(identity); err != nil {
		log.Errorf("failed to apply usb identity: %s", err)
		rsp.ErrRsp(c, -4, "failed to apply usb identity")
		return
	}

	rsp.OkRspWithData(c, &proto.GetUsbIdentityRsp{
		VendorID:     identity.VendorID,
		ProductID:    identity.ProductID,
		Manufacturer: identity.Manufacturer,
		Product:      identity.Product,
		Serial:       identity.Serial,
		Preset:       matchPreset(*identity),
		Presets:      listPresets(),
	})

	log.Debugf("set usb identity: %s:%s %q", identity.VendorID, identity.ProductID, identity.Product)
}

// RestoreUsbIdentity re-applies the stored identity at startup, because the boot
// scripts recreate the gadget with the stock descriptor. It is a no-op when no
// override was ever configured or when the gadget already matches.
func RestoreUsbIdentity() {
	usbIdentityMu.Lock()
	defer usbIdentityMu.Unlock()

	stored, err := loadStoredIdentity()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Errorf("failed to load stored usb identity: %s", err)
		}
		return
	}

	current, err := readGadgetIdentity()
	if err != nil {
		log.Debugf("usb gadget not available, skip identity restore: %s", err)
		return
	}

	if *current == *stored {
		log.Debugf("usb identity already applied, skip restore")
		return
	}

	// The snapshot has to happen before the first write, otherwise the factory
	// descriptor is lost.
	if err := snapshotFactoryIdentity(); err != nil {
		log.Errorf("failed to snapshot factory usb identity: %s", err)
	}

	if err := applyIdentity(stored); err != nil {
		log.Errorf("failed to restore usb identity: %s", err)
		return
	}

	log.Infof("restored usb identity: %s:%s %q", stored.VendorID, stored.ProductID, stored.Product)
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
	if name == PresetFactory {
		return factoryIdentity(), nil
	}

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
			Description: "Restore the original NanoKVM descriptor",
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
	if identity == *factoryIdentity() {
		return PresetFactory
	}

	for _, preset := range usbPresets {
		if identity == preset.Identity {
			return preset.Name
		}
	}

	return PresetCustom
}

func factoryIdentity() *UsbIdentity {
	identity, err := readIdentityFile(usbFactoryFile)
	if err != nil {
		fallback := sipeedIdentity
		return &fallback
	}
	return identity
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
		// configfs stores one line per attribute, so control characters would
		// either truncate the value or be rejected by the kernel.
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return errors.New("contains control characters")
		}
	}

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

func writeGadgetAttr(name string, value string) error {
	// configfs strips the trailing newline, which is also how an empty string
	// descriptor is written.
	return os.WriteFile(filepath.Join(gadgetDir, name), []byte(value+"\n"), 0o644)
}

// applyIdentity rewrites the gadget descriptor. The gadget has to be detached
// from the UDC first: while it is bound, configfs rejects writes to these
// attributes. The host sees the result as a re-plug of the composite device.
func applyIdentity(identity *UsbIdentity) error {
	udc, err := currentUDC()
	if err != nil {
		return err
	}

	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	if err := os.WriteFile(gadgetUDC, []byte("\n"), 0o644); err != nil {
		return fmt.Errorf("failed to unbind gadget: %w", err)
	}

	// Give the host a moment to notice the disconnect before it is offered a
	// device with a different identity on the same port.
	time.Sleep(500 * time.Millisecond)

	attrs := []struct {
		name  string
		value string
	}{
		{"idVendor", identity.VendorID},
		{"idProduct", identity.ProductID},
		{"strings/0x409/manufacturer", identity.Manufacturer},
		{"strings/0x409/product", identity.Product},
		{"strings/0x409/serialnumber", identity.Serial},
	}

	var writeErr error
	for _, attr := range attrs {
		if err := writeGadgetAttr(attr.name, attr.value); err != nil {
			writeErr = fmt.Errorf("failed to write %s: %w", attr.name, err)
			break
		}
	}

	// Rebind even when a write failed, otherwise the host is left without any
	// keyboard or mouse at all.
	if err := os.WriteFile(gadgetUDC, []byte(udc+"\n"), 0o644); err != nil {
		if writeErr != nil {
			return writeErr
		}
		return fmt.Errorf("failed to rebind gadget: %w", err)
	}

	if writeErr != nil {
		return writeErr
	}

	// The HID nodes reappear asynchronously after the rebind.
	time.Sleep(2 * time.Second)

	return nil
}

func currentUDC() (string, error) {
	entries, err := os.ReadDir(udcClassDir)
	if err != nil {
		return "", fmt.Errorf("failed to list %s: %w", udcClassDir, err)
	}

	for _, entry := range entries {
		return entry.Name(), nil
	}

	return "", errors.New("no usb device controller found")
}

func snapshotFactoryIdentity() error {
	if _, err := os.Stat(usbFactoryFile); err == nil {
		return nil
	}

	// A stored override means the gadget was already rewritten at least once,
	// so the running descriptor is not the factory one any more.
	if _, err := os.Stat(usbIdentityFile); err == nil {
		return nil
	}

	identity, err := readGadgetIdentity()
	if err != nil {
		return err
	}

	return writeIdentityFile(usbFactoryFile, identity)
}

func loadStoredIdentity() (*UsbIdentity, error) {
	return readIdentityFile(usbIdentityFile)
}

func saveStoredIdentity(identity *UsbIdentity) error {
	return writeIdentityFile(usbIdentityFile, identity)
}

func readIdentityFile(path string) (*UsbIdentity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var identity UsbIdentity
	if err := json.Unmarshal(data, &identity); err != nil {
		return nil, fmt.Errorf("failed to decode %s: %w", path, err)
	}

	if err := validateIdentity(&identity); err != nil {
		return nil, fmt.Errorf("invalid identity in %s: %w", path, err)
	}

	return &identity, nil
}

func writeIdentityFile(path string, identity *UsbIdentity) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, append(data, '\n'), 0o644)
}
