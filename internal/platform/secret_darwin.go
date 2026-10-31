//go:build darwin

package platform

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
#include <Security/Security.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>

static CFMutableDictionaryRef tacet_query(const char *service, const char *account) {
	CFMutableDictionaryRef q = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFStringRef s = CFStringCreateWithCString(NULL, service, kCFStringEncodingUTF8);
	CFStringRef a = CFStringCreateWithCString(NULL, account, kCFStringEncodingUTF8);
	CFDictionarySetValue(q, kSecClass, kSecClassGenericPassword);
	CFDictionarySetValue(q, kSecAttrService, s);
	CFDictionarySetValue(q, kSecAttrAccount, a);
	CFRelease(s);
	CFRelease(a);
	return q;
}

static OSStatus tacet_add(const char *service, const char *account, const void *data, long len) {
	CFMutableDictionaryRef q = tacet_query(service, account);
	CFDataRef d = CFDataCreate(NULL, data, len);
	CFDictionarySetValue(q, kSecValueData, d);
	// usable only while this device is unlocked, never synced or backed up elsewhere
	CFDictionarySetValue(q, kSecAttrAccessible, kSecAttrAccessibleWhenUnlockedThisDeviceOnly);
	OSStatus st = SecItemAdd(q, NULL);
	CFRelease(d);
	CFRelease(q);
	return st;
}

static OSStatus tacet_load(const char *service, const char *account, void **out, long *outLen) {
	CFMutableDictionaryRef q = tacet_query(service, account);
	CFDictionarySetValue(q, kSecReturnData, kCFBooleanTrue);
	CFDictionarySetValue(q, kSecMatchLimit, kSecMatchLimitOne);
	CFTypeRef result = NULL;
	OSStatus st = SecItemCopyMatching(q, &result);
	CFRelease(q);
	if (st == errSecSuccess && result != NULL) {
		CFDataRef d = (CFDataRef)result;
		*outLen = CFDataGetLength(d);
		*out = malloc(*outLen > 0 ? *outLen : 1);
		memcpy(*out, CFDataGetBytePtr(d), *outLen);
		CFRelease(result);
	}
	return st;
}

static OSStatus tacet_delete(const char *service, const char *account) {
	CFMutableDictionaryRef q = tacet_query(service, account);
	OSStatus st = SecItemDelete(q);
	CFRelease(q);
	return st;
}
*/
import "C"

import (
	"unsafe"
)

func status(st C.OSStatus) error {
	switch st {
	case C.errSecSuccess:
		return nil
	case C.errSecItemNotFound:
		return ErrSecretNotFound
	case C.errSecDuplicateItem:
		return ErrSecretExists
	}
	return ErrSecretUnavailable // locked, no session, access denied...: stop, never fall back
}

func (s *Secret) cstrings() (*C.char, *C.char) {
	return C.CString(s.service), C.CString(s.account)
}

func (s *Secret) Save(value []byte) error {
	if len(value) == 0 {
		return ErrSecretUnavailable
	}
	svc, acct := s.cstrings()
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	return status(C.tacet_add(svc, acct, unsafe.Pointer(&value[0]), C.long(len(value))))
}

func (s *Secret) Load() ([]byte, error) {
	svc, acct := s.cstrings()
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	var out unsafe.Pointer
	var n C.long
	if err := status(C.tacet_load(svc, acct, &out, &n)); err != nil {
		return nil, err
	}
	defer C.free(out)
	value := C.GoBytes(out, C.int(n))
	C.memset(out, 0, C.size_t(n)) // the C copy never lingers
	return value, nil
}

func (s *Secret) Delete() error {
	svc, acct := s.cstrings()
	defer C.free(unsafe.Pointer(svc))
	defer C.free(unsafe.Pointer(acct))
	err := status(C.tacet_delete(svc, acct))
	if err == ErrSecretNotFound {
		return nil // idempotent
	}
	return err
}
