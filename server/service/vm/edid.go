package vm

import (
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	EdidDir            = "/kvmcomm/edid"
	LT6911Edid         = "/proc/lt6911_info/edid"
	LT6911EdidSnapshot = "/proc/lt6911_info/edid_snapshot"

	CustomEdidDir  = "/etc/kvm/edid"
	CustomEdidFlag = "/etc/kvm/edid/edid_flag"
)

var EDIDMap = map[byte]string{
	0x12: "E18-4K30FPS",
	0x30: "E48-4K39FPS",
	0x36: "E54-1080P60FPS",
	0x38: "E56-2K60FPS",
	0x3a: "E58-4K16-10",
	0x3f: "E63-Ultrawide",

	// Profiles shipped with the server under bundledEdidDir(). The key is byte 12
	// of the EDID (the low byte of the serial number), which is what the LT6911
	// snapshot exposes for identification.
	0x4c: "LG-24BK550Y-B",
}

// bundledEdidDir returns the directory of EDID profiles shipped alongside the
// server binary, next to the web assets that router.web() serves.
func bundledEdidDir() string {
	bundledEdidDirOnce.Do(func() {
		execPath, err := os.Executable()
		if err != nil {
			log.Errorf("failed to resolve executable path: %s", err)
			return
		}
		bundledEdidDirPath = filepath.Join(filepath.Dir(execPath), "edid")
	})
	return bundledEdidDirPath
}

var (
	bundledEdidDirOnce sync.Once
	bundledEdidDirPath string
)

// resolveEdidPath locates the blob for the requested profile. Factory profiles
// and the bundled ones are stored with a .bin suffix, uploaded ones keep the
// filename they were uploaded under.
func resolveEdidPath(name string) (path string, isCustom bool, err error) {
	if !isSafeEdidName(name) {
		return "", false, fmt.Errorf("invalid EDID name: %s", name)
	}

	type candidate struct {
		path   string
		custom bool
	}

	candidates := []candidate{
		{filepath.Join(EdidDir, name+".bin"), false},
	}

	if dir := bundledEdidDir(); dir != "" {
		candidates = append(candidates, candidate{filepath.Join(dir, name+".bin"), false})
	}

	candidates = append(candidates, candidate{filepath.Join(CustomEdidDir, name), true})

	for _, c := range candidates {
		if _, statErr := os.Stat(c.path); statErr == nil {
			return c.path, c.custom, nil
		}
	}

	return "", false, fmt.Errorf("unknown EDID: %s", name)
}

func (s *Service) GetEdid(ctx *gin.Context) {
	var resp proto.Response
	content, err := os.ReadFile(LT6911EdidSnapshot)
	if err != nil {
		resp.ErrRsp(ctx, -1, "get edid failed")
		return
	}

	if len(content) <= 12 {
		log.Errorf("invalid EDID snapshot length: %d", len(content))
		resp.ErrRsp(ctx, -1, "get edid failed")
		return
	}

	edid, ok := EDIDMap[content[12]]
	if !ok {
		// custom EDID
		if flag, err := os.ReadFile(CustomEdidFlag); err == nil {
			edid = strings.TrimSpace(string(flag))
		}
	}

	resp.OkRspWithData(ctx, gin.H{
		"edid": edid,
	})
}

func (s *Service) SwitchEdid(c *gin.Context) {
	var req proto.SwitchEdidReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if req.Edid == "" {
		rsp.ErrRsp(c, -2, "invalid EDID")
		return
	}

	srcPath, isCustom, err := resolveEdidPath(req.Edid)
	if err != nil {
		log.Debugf("unknown edid: %s", req.Edid)
		rsp.ErrRsp(c, -3, "invalid EDID")
		return
	}

	if isCustom {
		// Uploaded profiles carry no marker byte the snapshot could be matched
		// against, so remember which one is active.
		_ = os.WriteFile(CustomEdidFlag, []byte(req.Edid), 0644)
	}

	if err := copyFile(srcPath, LT6911Edid); err != nil {
		log.Errorf("failed to switch EDID %s: %s", req.Edid, err)
		rsp.ErrRsp(c, -4, "failed to switch EDID")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("switch edid %s", req.Edid)
}

func (s *Service) GetCustomEdidList(c *gin.Context) {
	var rsp proto.Response

	var files []string

	err := filepath.Walk(CustomEdidDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() && isBin(info.Name()) {
			files = append(files, info.Name())
		}

		return nil
	})
	if err != nil {
		rsp.ErrRsp(c, -1, "get EDID failed")
		return
	}

	rsp.OkRspWithData(c, &proto.GetCustomEdidListRsp{
		EdidList: files,
	})
}

func (s *Service) DeleteEdid(c *gin.Context) {
	var req proto.DeleteEdidReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if !isSafeEdidName(req.Edid) {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	file := filepath.Join(CustomEdidDir, req.Edid)

	if err := os.Remove(file); err != nil {
		log.Errorf("delete edid %s failed: %s", req.Edid, err)
		rsp.ErrRsp(c, -2, "delete failed")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("delete edid %s success", req.Edid)
}

func (s *Service) UploadEdid(c *gin.Context) {
	var rsp proto.Response

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		rsp.ErrRsp(c, -1, "bad request")
		return
	}
	defer file.Close()

	if _, err = os.Stat(CustomEdidDir); err != nil {
		_ = os.MkdirAll(CustomEdidDir, 0o755)
	}

	name := filepath.Base(header.Filename)
	if !isSafeEdidName(name) {
		rsp.ErrRsp(c, -1, "bad request")
		return
	}

	target := fmt.Sprintf("%s/%s", CustomEdidDir, name)
	dst, err := os.Create(target)
	if err != nil {
		rsp.ErrRsp(c, -2, "create file failed")
		return
	}
	defer dst.Close()

	buf := make([]byte, 32*1024)
	_, err = io.CopyBuffer(dst, file, buf)
	if err != nil {
		rsp.ErrRsp(c, -3, "save failed")
		return
	}

	_ = utils.EnsurePermission(target, 0o644)

	data := &proto.UploadEdidRsp{
		File: name,
	}
	rsp.OkRspWithData(c, data)
	log.Debugf("upload edid file: %s", name)
}

func copyFile(src, dst string) error {
	input, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, input, 0644)
}

// isSafeEdidName rejects anything that would let a profile name escape the
// directory it is looked up in; these names are joined onto filesystem paths.
func isSafeEdidName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, `/\`)
}

func isBin(name string) bool {
	nameLower := strings.ToLower(name)
	if strings.HasSuffix(nameLower, ".bin") {
		return true
	}

	return false
}
