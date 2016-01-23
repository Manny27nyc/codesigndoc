package cli

import (
	"fmt"
	"os"
	"path"

	log "github.com/Sirupsen/logrus"

	"github.com/bitrise-io/go-utils/cmdex"
	"github.com/bitrise-io/go-utils/colorstring"
	"github.com/bitrise-io/go-utils/pathutil"
	"github.com/bitrise-io/goinp/goinp"
	"github.com/bitrise-tools/codesigndoc/osxkeychain"
	"github.com/bitrise-tools/codesigndoc/provprofile"
	"github.com/bitrise-tools/codesigndoc/utils"
	"github.com/bitrise-tools/codesigndoc/xcode"
	"github.com/codegangsta/cli"
)

const (
	confExportOutputDirPath = "./codesigndoc_exports"
)

func printFinished() {
	fmt.Println()
	fmt.Println(colorstring.Green("That's all."))
	fmt.Println("You just have to upload the found code signing files and you'll be good to go!")
}

func scan(c *cli.Context) {
	projectPath := c.String(FileParamKey)
	if projectPath == "" {
		askText := `Please drag-and-drop your Xcode Project (` + colorstring.Green(".xcodeproj") + `)
   or Workspace (` + colorstring.Green(".xcworkspace") + `) file, the one you usually open in Xcode,
   then hit Enter.

  (Note: if you have a Workspace file you should most likely use that)`
		fmt.Println()
		projpth, err := goinp.AskForString(askText)
		if err != nil {
			log.Fatalf("Failed to read input: %s", err)
		}
		projectPath = projpth
	}
	log.Debugf("projectPath: %s", projectPath)
	xcodeCmd := xcode.CommandModel{
		ProjectFilePath: projectPath,
	}

	schemeToUse := c.String(SchemeParamKey)
	if schemeToUse == "" {
		log.Println("🔦  Scanning Schemes ...")
		schemes, err := xcodeCmd.ScanSchemes()
		if err != nil {
			log.Fatalf("Failed to scan Schemes: %s", err)
		}
		log.Debugf("schemes: %v", schemes)

		fmt.Println()
		selectedScheme, err := goinp.SelectFromStrings("Select the Scheme you usually use in Xcode", schemes)
		if err != nil {
			log.Fatalf("Failed to select Scheme: %s", err)
		}
		log.Debugf("selected scheme: %v", selectedScheme)
		schemeToUse = selectedScheme
	}
	xcodeCmd.Scheme = schemeToUse

	fmt.Println()
	log.Println("🔦  Running an Xcode Archive, to get all the required code signing settings...")
	codeSigningSettings, err := xcodeCmd.ScanCodeSigningSettings()
	if err != nil {
		log.Fatalf("Failed to detect code signing settings: %s", err)
	}
	log.Debugf("codeSigningSettings: %#v", codeSigningSettings)

	fmt.Println()
	utils.Printlnf("=== Required Identities/Certificates (%d) ===", len(codeSigningSettings.Identities))
	for idx, anIdentity := range codeSigningSettings.Identities {
		utils.Printlnf(" * (%d): %s", idx+1, anIdentity.Title)
	}
	fmt.Println("========================================")

	fmt.Println()
	utils.Printlnf("=== Required Provisioning Profiles (%d) ===", len(codeSigningSettings.ProvProfiles))
	for idx, aProvProfile := range codeSigningSettings.ProvProfiles {
		utils.Printlnf(" * (%d): %s (UUID: %s)", idx+1, aProvProfile.Title, aProvProfile.UUID)
	}
	fmt.Println("======================================")

	//
	// --- Code Signing issue checks / report
	//

	if len(codeSigningSettings.Identities) < 1 {
		log.Fatal("No Code Signing Identity detected!")
	}
	if len(codeSigningSettings.Identities) > 1 {
		log.Warning("More than one Code Signing Identity (certificate) is required to sign your app!")
		log.Warning("You should check your settings and make sure a single Identity/Certificate can be used")
		log.Warning(" for Archiving your app!")
	}

	if len(codeSigningSettings.ProvProfiles) < 1 {
		log.Fatal("No Provisioning Profiles detected!")
	}

	//
	// --- Export
	//

	isShouldExport, err := goinp.AskForBool("Do you want to export these files?")
	if err != nil {
		log.Fatalf("Failed to process your input: %s", err)
	}
	if !isShouldExport {
		printFinished()
		return
	}

	fmt.Println()
	log.Println("Exporting the required Identities (Certificates) ...")
	fmt.Println(" You'll most likely see popups (one for each Identity) from Keychain,")
	fmt.Println(" you will have to accept (Allow) those to be able to export the Identity")
	fmt.Println()

	absExportOutputDirPath, err := pathutil.AbsPath(confExportOutputDirPath)
	log.Debugf("absExportOutputDirPath: %s", absExportOutputDirPath)
	if err != nil {
		log.Fatalf("Failed to determin Absolute path of export dir: %s", confExportOutputDirPath)
	}
	if exist, err := pathutil.IsDirExists(absExportOutputDirPath); err != nil {
		log.Fatalf("Failed to determin whether the export directory already exists: %s", err)
	} else if !exist {
		if err := os.Mkdir(absExportOutputDirPath, 0777); err != nil {
			log.Fatalf("Failed to create export output directory at path: %s | error: %s", absExportOutputDirPath, err)
		}
	} else {
		log.Debugf("Export output dir already exists at path: %s", absExportOutputDirPath)
	}

	identityExportRefs := osxkeychain.CreateEmptyCFTypeRefSlice()
	defer osxkeychain.ReleaseRefList(identityExportRefs)

	fmt.Println()
	for _, aIdentity := range codeSigningSettings.Identities {
		log.Infof(" * Exporting Identity: %s", aIdentity.Title)
		identityRefs, err := osxkeychain.FindIdentity(aIdentity.Title)
		if err != nil {
			log.Fatalf("Failed to Export Identity: %s", err)
		}
		log.Debugf("identityRefs: %d", len(identityRefs))
		if len(identityRefs) < 1 {
			log.Fatalf("No Identity found in Keychain!")
		}
		if len(identityRefs) > 1 {
			log.Fatalf("Multiple matching Identities found in Keychain! Most likely you have duplicate identity in separate Keychains, like one in System.keychain and one in your Login.keychain")
		}
		identityExportRefs = append(identityExportRefs, identityRefs...)
	}

	if err := osxkeychain.ExportFromKeychain(identityExportRefs, path.Join(absExportOutputDirPath, "Identities.p12")); err != nil {
		log.Fatalf("Failed to export from Keychain: %s", err)
	}

	fmt.Println()
	for _, aProvProfile := range codeSigningSettings.ProvProfiles {
		log.Infof(" * Exporting Provisioning Profile: %s (UUID: %s)", aProvProfile.Title, aProvProfile.UUID)
		filePth, err := provprofile.FindProvProfileFile(aProvProfile)
		if err != nil {
			log.Fatalf("Failed to find Provisioning Profile: %s", err)
		}
		log.Infof("  File found at: %s", filePth)

		cmdex.RunCommandAndReturnCombinedStdoutAndStderr("cp", filePth, absExportOutputDirPath+"/")

		// if err := provprofile.PrintFileInfo(filePth); err != nil {
		// 	log.Fatalf("Err: %s", err)
		// }
	}

	fmt.Println()
	fmt.Printf(colorstring.Green("Exports finished")+" you can find the exported files at: %s", absExportOutputDirPath)
	if err := cmdex.RunCommand("open", absExportOutputDirPath); err != nil {
		log.Errorf("Failed to open the export directory in Finder: %s", absExportOutputDirPath)
	}
	fmt.Println("Opened the directory in Finder.")
	fmt.Println()

	printFinished()
}