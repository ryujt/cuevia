//go:build darwin && cgo

#include <Carbon/Carbon.h>
#include <ApplicationServices/ApplicationServices.h>
#include <pthread.h>
#include "native_darwin.h"

extern void cueviaCapturePressed(void);
extern int cuevia_dispatch_cocoa_event(void);

static EventHotKeyRef capture_hotkey = NULL;
static EventHandlerRef capture_handler = NULL;
static const OSType capture_signature = 'Cuvi';

static OSStatus capture_pressed(EventHandlerCallRef next, EventRef event, void *context) {
    (void)next;
    (void)context;
    EventHotKeyID identifier;
    OSStatus status = GetEventParameter(event, kEventParamDirectObject,
        typeEventHotKeyID, NULL, sizeof(identifier), NULL, &identifier);
    if (status != noErr) return status;
    if (identifier.signature != capture_signature || identifier.id != 1)
        return eventNotHandledErr;
    cueviaCapturePressed();
    return noErr;
}

int cuevia_start_hotkey(void) {
    if (!pthread_main_np()) return -20001;
    OSStatus status;
    EventTypeSpec pressed = {kEventClassKeyboard, kEventHotKeyPressed};
    status = InstallApplicationEventHandler(capture_pressed, 1, &pressed,
        NULL, &capture_handler);
    if (status != noErr) return (int)status;

    EventHotKeyID identifier = {capture_signature, 1};
    // Virtual key 1 is the physical S key. Carbon hotkeys require no event tap
    // or Accessibility permission and remain active in other applications.
    status = RegisterEventHotKey(1, cmdKey | controlKey, identifier,
        GetApplicationEventTarget(), kEventHotKeyExclusive, &capture_hotkey);
    if (status != noErr) {
        RemoveEventHandler(capture_handler);
        capture_handler = NULL;
    }
    return (int)status;
}

int cuevia_dispatch_next_event(void) {
    // AppKit owns modal stop/quit events. Dispatching its queue manually through
    // Carbon turns a normal NSAlert close into eventLoopQuitErr (-9876).
    // AppKit's event fetch also dispatches registered Carbon hotkey handlers.
    return cuevia_dispatch_cocoa_event();
}

void cuevia_stop_hotkey(void) {
    if (capture_hotkey != NULL) {
        UnregisterEventHotKey(capture_hotkey);
        capture_hotkey = NULL;
    }
    if (capture_handler != NULL) {
        RemoveEventHandler(capture_handler);
        capture_handler = NULL;
    }
}

int cuevia_screen_capture_allowed(void) {
    // This is a read-only check. It never requests or changes system permission.
    return CGPreflightScreenCaptureAccess() ? 1 : 0;
}
