package osxkeychain

import (
	"errors"
	"fmt"
	"unsafe"

	log "github.com/Sirupsen/logrus"
	"github.com/bitrise-io/go-utils/fileutil"
)

/*
#cgo CFLAGS: -mmacosx-version-min=10.7 -D__MAC_OS_X_VERSION_MAX_ALLOWED=1060
#cgo LDFLAGS: -framework CoreFoundation -framework Security
#include <stdlib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
*/
import "C"

// ExportFromKeychain ...
func ExportFromKeychain(itemRefsToExport []C.CFTypeRef, outputFilePath string) error {
	log.Info("Exporting from Keychain, using empty Passphrase ...")

	passphraseCString := C.CString("")
	defer C.free(unsafe.Pointer(passphraseCString))

	var exportedData C.CFDataRef
	var exportParams C.SecItemImportExportKeyParameters
	exportParams.passphrase = (C.CFTypeRef)(convertCStringToCFString(passphraseCString))
	exportParams.keyUsage = nil
	exportParams.keyAttributes = nil
	exportParams.version = C.SEC_KEY_IMPORT_EXPORT_PARAMS_VERSION
	exportParams.flags = 0
	exportParams.alertTitle = nil
	exportParams.alertPrompt = nil

	// create a C array from the input
	ptr := (*unsafe.Pointer)(&itemRefsToExport[0])
	cfArrayForExport := C.CFArrayCreate(
		C.kCFAllocatorDefault,
		ptr,
		C.CFIndex(len(itemRefsToExport)),
		&C.kCFTypeArrayCallBacks)

	// do the export!
	status := C.SecItemExport(C.CFTypeRef(cfArrayForExport),
		C.kSecFormatPKCS12,
		C.kSecItemPemArmour, /* Use kSecItemPemArmour to add PEM armor */
		&exportParams,
		&exportedData)

	if status != C.errSecSuccess {
		return fmt.Errorf("SecItemExport: error (OSStatus): %d", status)
	}
	// exportedData now contains your PKCS12 data
	//  make sure it'll be released properly!
	defer C.CFRelease(C.CFTypeRef(exportedData))

	dataBytes := C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(exportedData)), (C.int)(C.CFDataGetLength(exportedData)))
	log.Debugf("dataBytes: %#v", dataBytes)

	if err := fileutil.WriteBytesToFile(outputFilePath, dataBytes); err != nil {
		return fmt.Errorf("ExportFromKeychain: failed to write into file: %s", err)
	}

	log.Debug("Export - success")

	return nil
}

// ReleaseRef ...
func ReleaseRef(refItem C.CFTypeRef) {
	C.CFRelease(refItem)
}

// ReleaseRefList ...
func ReleaseRefList(refItems []C.CFTypeRef) {
	for _, itm := range refItems {
		ReleaseRef(itm)
	}
}

// CreateEmptyCFTypeRefSlice ...
func CreateEmptyCFTypeRefSlice() []C.CFTypeRef {
	return []C.CFTypeRef{}
}

// FindIdentity ...
//  IMPORTANT: you have to C.CFRelease the returned items (one-by-one)!!
//             you can use the ReleaseRefList method to do that
func FindIdentity(identityLabel string) ([]C.CFTypeRef, error) {

	queryDict := C.CFDictionaryCreateMutable(nil, 0, nil, nil)
	defer C.CFRelease(C.CFTypeRef(queryDict))
	C.CFDictionaryAddValue(queryDict, unsafe.Pointer(C.kSecClass), unsafe.Pointer(C.kSecClassIdentity))
	C.CFDictionaryAddValue(queryDict, unsafe.Pointer(C.kSecMatchLimit), unsafe.Pointer(C.kSecMatchLimitAll))
	C.CFDictionaryAddValue(queryDict, unsafe.Pointer(C.kSecReturnAttributes), unsafe.Pointer(C.kCFBooleanTrue))
	C.CFDictionaryAddValue(queryDict, unsafe.Pointer(C.kSecReturnRef), unsafe.Pointer(C.kCFBooleanTrue))

	var resultRefs C.CFTypeRef
	osStatusCode := C.SecItemCopyMatching(queryDict, &resultRefs)
	if osStatusCode != C.errSecSuccess {
		return nil, fmt.Errorf("Failed to call SecItemCopyMatch - OSStatus: %d", osStatusCode)
	}
	defer C.CFRelease(C.CFTypeRef(resultRefs))

	identitiesArrRef := C.CFArrayRef(resultRefs)
	identitiesCount := C.CFArrayGetCount(identitiesArrRef)
	if identitiesCount < 1 {
		return nil, fmt.Errorf("No Identity found in your Keychain with the specified Label!")
	}
	log.Debugf("identitiesCount: %d", identitiesCount)

	// filter the identities, by label
	retIdentityRefs := []C.CFTypeRef{}
	for i := identitiesCount - 1; i > 0; i-- {
		aIdentityRef := C.CFArrayGetValueAtIndex(identitiesArrRef, i)
		log.Debugf("aIdentityRef: %#v", aIdentityRef)
		aIdentityDictRef := C.CFDictionaryRef(aIdentityRef)
		log.Debugf("aIdentityDictRef: %#v", aIdentityDictRef)

		lablCSting := C.CString("labl")
		defer C.free(unsafe.Pointer(lablCSting))
		vrefCSting := C.CString("v_Ref")
		defer C.free(unsafe.Pointer(vrefCSting))

		labl, err := getCFDictValueUTF8String(aIdentityDictRef, C.CFTypeRef(convertCStringToCFString(lablCSting)))
		if err != nil {
			return nil, fmt.Errorf("FindIdentity: failed to get 'labl' property: %s", err)
		}
		log.Debugf("labl: %#v", labl)
		if labl != identityLabel {
			continue
		}
		log.Debugf("Found identity with label: %s", labl)

		vrefRef, err := getCFDictValueRef(aIdentityDictRef, C.CFTypeRef(convertCStringToCFString(vrefCSting)))
		if err != nil {
			return nil, fmt.Errorf("FindIdentity: failed to get 'v_Ref' property: %s", err)
		}
		log.Debugf("vrefRef: %#v", vrefRef)

		// retain the pointer
		vrefRef = C.CFRetain(vrefRef)
		// store it
		retIdentityRefs = append(retIdentityRefs, vrefRef)
	}

	fmt.Println("-- DONE --")
	return retIdentityRefs, nil
}

//
// --- UTIL METHODS
//

func getCFDictValueRef(dict C.CFDictionaryRef, key C.CFTypeRef) (C.CFTypeRef, error) {
	var retVal C.CFTypeRef
	exist := C.CFDictionaryGetValueIfPresent(dict, unsafe.Pointer(key), (*unsafe.Pointer)(retVal))
	// log.Debugf("retVal: %#v", retVal)
	if exist == C.Boolean(0) {
		return nil, errors.New("getCFDictValueRef: Key doesn't exist")
	}
	// return retVal, nil

	return (C.CFTypeRef)(C.CFDictionaryGetValue(dict, unsafe.Pointer(key))), nil
}

func getCFDictValueCFStringRef(dict C.CFDictionaryRef, key C.CFTypeRef) (C.CFStringRef, error) {
	val, err := getCFDictValueRef(dict, key)
	if err != nil {
		return nil, err
	}
	if val == nil {
		return nil, errors.New("getCFDictValueCFStringRef: Nil value returned")
	}

	if C.CFGetTypeID(val) != C.CFStringGetTypeID() {
		return nil, errors.New("getCFDictValueCFStringRef: value is not a string")
	}

	return C.CFStringRef(val), nil
}

func convertCStringToCFString(cstring *C.char) C.CFStringRef {
	return C.CFStringCreateWithCString(C.kCFAllocatorDefault, cstring, C.kCFStringEncodingUTF8)
}

func getCFDictValueUTF8String(dict C.CFDictionaryRef, key C.CFTypeRef) (string, error) {
	valCFStringRef, err := getCFDictValueCFStringRef(dict, key)
	if err != nil {
		return "", err
	}
	log.Debugf("valCFStringRef: %#v", valCFStringRef)
	if valCFStringRef == nil {
		return "", errors.New("getCFDictValueUTF8String: Nil value")
	}

	cstr := C.CFStringGetCStringPtr(valCFStringRef, C.kCFStringEncodingUTF8)
	log.Debugf("cstr: %#v", cstr)
	if cstr == nil {
		return "", errors.New("getCFDictValueUTF8String: failed to convert value to string")
	}

	return C.GoString(cstr), nil
}
