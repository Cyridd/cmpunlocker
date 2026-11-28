// winres_gen writes the Windows version resources and the installer's UI manifest.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

const windowsVersion = "2.2.0.0"

func main() {
	tool := flag.String("tool", "inst40hx", "Go module name of the Windows tool")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: winres_gen [-tool module] <out.syso>")
		os.Exit(1)
	}

	rs := winres.ResourceSet{}
	vi := version.Info{}
	vi.SetFileVersion(windowsVersion)
	vi.SetProductVersion(windowsVersion)
	var filename, description string
	switch *tool {
	case "inst40hx":
		filename, description = "40HXInstaller.exe", "40HX Installer/Manager"
		rs.SetManifest(winres.AppManifest{
			Identity:            winres.AssemblyIdentity{Name: "40HX.Installer", Version: vi.FileVersion},
			Description:         "CMP 40HX Unlock Manager (compute unlock + PCIe Gen2)",
			UseCommonControlsV6: true,
			DPIAwareness:        winres.DPIPerMonitorV2,
			ExecutionLevel:      winres.AsInvoker,
		})
	case "check40x":
		filename, description = "40HXCheck.exe", "40HX Diagnostics"
	case "uninstall40x":
		filename, description = "40HXUninstaller.exe", "40HX Uninstaller"
	case "winres_gen":
		filename, description = "winresgen.exe", "40HX Windows resource generator"
	default:
		fmt.Fprintln(os.Stderr, "unknown Go module:", *tool)
		os.Exit(1)
	}
	vi.Set(winres.LCIDDefault, "ProductName", "CMP 40HX Windows Unlock")
	vi.Set(winres.LCIDDefault, "FileDescription", description)
	vi.Set(winres.LCIDDefault, "LegalCopyright", "For personal hardware research and study only")
	vi.Set(winres.LCIDDefault, "OriginalFilename", filename)
	rs.SetVersionInfo(vi)
	f, err := os.Create(flag.Arg(0))
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := rs.WriteObject(f, winres.ArchAMD64); err != nil {
		panic(err)
	}
	fmt.Println("written:", flag.Arg(0))
}
