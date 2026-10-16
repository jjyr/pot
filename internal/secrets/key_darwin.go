//go:build darwin && cgo

package secrets

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
static OSStatus pot_read_key(const char *account, unsigned char *result, long *size) {
 CFStringRef acct=CFStringCreateWithCString(NULL,account,kCFStringEncodingUTF8);
 if(!acct) return errSecParam;
 const void *keys[]={kSecClass,kSecAttrService,kSecAttrAccount,kSecReturnData,kSecMatchLimit};
 const void *values[]={kSecClassGenericPassword,CFSTR("pot.control.secrets"),acct,kCFBooleanTrue,kSecMatchLimitOne};
 CFDictionaryRef query=CFDictionaryCreate(NULL,keys,values,5,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 CFTypeRef value=NULL;OSStatus status=SecItemCopyMatching(query,&value);
 if(status==errSecSuccess) {
  if(CFGetTypeID(value)!=CFDataGetTypeID())status=errSecDecode;
  else {*size=CFDataGetLength((CFDataRef)value);if(*size==32)memcpy(result,CFDataGetBytePtr((CFDataRef)value),32);}
 }
 if(value)CFRelease(value);CFRelease(query);CFRelease(acct);return status;
}
static OSStatus pot_add_key(const char *account,const unsigned char *key) {
 CFStringRef acct=CFStringCreateWithCString(NULL,account,kCFStringEncodingUTF8);
 if(!acct) return errSecParam;
 CFDataRef data=CFDataCreate(NULL,key,32);
 const void *keys[]={kSecClass,kSecAttrService,kSecAttrAccount,kSecValueData};
 const void *values[]={kSecClassGenericPassword,CFSTR("pot.control.secrets"),acct,data};
 CFDictionaryRef query=CFDictionaryCreate(NULL,keys,values,4,&kCFTypeDictionaryKeyCallBacks,&kCFTypeDictionaryValueCallBacks);
 OSStatus status=SecItemAdd(query,NULL);CFRelease(query);CFRelease(data);CFRelease(acct);return status;
}
*/
import "C"

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"
)

var keychainMu sync.Mutex

func platformKey(ctx context.Context, account string, allowCreate bool) ([]byte, error) {
	if strings.ContainsRune(account, '\x00') {
		return nil, errors.New("invalid Keychain account")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keychainMu.Lock()
	defer keychainMu.Unlock()
	acct := C.CString(account)
	defer C.free(unsafe.Pointer(acct))
	read := func() ([]byte, C.OSStatus) {
		key := make([]byte, 32)
		var size C.long
		status := C.pot_read_key(acct, (*C.uchar)(unsafe.Pointer(&key[0])), &size)
		if status != 0 || size != 32 {
			clear(key)
			if status == 0 {
				status = C.errSecDecode
			}
			return nil, status
		}
		return key, status
	}
	key, status := read()
	if status == 0 {
		return key, nil
	}
	if status != C.errSecItemNotFound || !allowCreate {
		return nil, fmt.Errorf("Control secret key unavailable in Keychain (status %d)", int(status))
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	defer clear(key)
	status = C.pot_add_key(acct, (*C.uchar)(unsafe.Pointer(&key[0])))
	if status != 0 && status != C.errSecDuplicateItem {
		return nil, fmt.Errorf("Control secret key creation failed in Keychain (status %d)", int(status))
	}
	// Re-read the winner: concurrent Controls must never overwrite each other's key.
	value, status := read()
	if status != 0 {
		return nil, fmt.Errorf("Control secret key unavailable in Keychain (status %d)", int(status))
	}
	return value, nil
}
