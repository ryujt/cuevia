//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
#include <pthread.h>

int cuevia_request_screen_permission(int *allowed) {
 if(allowed)*allowed=0;
 if(!pthread_main_np())return -20001;
 // This is the OS consent API. macOS owns whether a prompt is displayed;
 // previously denied access can return false without presenting another one.
 bool granted=CGPreflightScreenCaptureAccess();
 if(!granted)granted=CGRequestScreenCaptureAccess();
 if(allowed)*allowed=granted?1:0;
 return 0;
}

int cuevia_show_capture_error(const char *message) {
 if(!pthread_main_np())return -20001;
 @autoreleasepool {
  NSApplicationLoad();
  NSRunningApplication *previous=[[NSWorkspace sharedWorkspace] frontmostApplication];
  NSApplication *app=[NSApplication sharedApplication];
  [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
  NSString *details=message?[NSString stringWithUTF8String:message]:nil;
  NSAlert *alert=[[NSAlert alloc] init];
  [alert setAlertStyle:NSAlertStyleWarning];
  [alert setMessageText:@"Couldn't capture the screen"];
  [alert setInformativeText:details.length?details:@"Please try again in a moment."];
  [alert addButtonWithTitle:@"OK"];
  [app activateIgnoringOtherApps:YES];
  [[alert window] setLevel:NSFloatingWindowLevel];
  [alert runModal];
  [[alert window] orderOut:nil];
  [app hide:nil];
  if(previous&&previous.processIdentifier!=[[NSProcessInfo processInfo] processIdentifier])
   [previous activateWithOptions:NSApplicationActivateIgnoringOtherApps];
  [alert release];
  return 0;
 }
}
