//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

char *cuevia_copy_sources(void) {
 @autoreleasepool {
  NSMutableArray *monitors=[NSMutableArray array];
  uint32_t count=0;CGGetActiveDisplayList(0,NULL,&count);
  CGDirectDisplayID *displays=calloc(count,sizeof(CGDirectDisplayID));
  if(count&&!displays)return NULL;
  CGGetActiveDisplayList(count,displays,&count);
  for(uint32_t i=0;i<count;i++) {
   CGDirectDisplayID display=displays[i];CGRect bounds=CGDisplayBounds(display);
   CGDisplayModeRef mode=CGDisplayCopyDisplayMode(display);
   size_t width=mode?CGDisplayModeGetPixelWidth(mode):CGDisplayPixelsWide(display);
   size_t height=mode?CGDisplayModeGetPixelHeight(mode):CGDisplayPixelsHigh(display);
   BOOL builtIn=CGDisplayIsBuiltin(display),main=CGDisplayIsMain(display);
   NSString *name=builtIn?@"Built-in display":[NSString stringWithFormat:@"External monitor %u",i+1];
   [monitors addObject:@{@"id":[NSString stringWithFormat:@"display-%u",display],@"kind":@"monitor",@"name":name,@"displayId":@(display),@"displayIndex":@(i+1),@"width":@(width),@"height":@(height),@"scale":@(bounds.size.width?width/bounds.size.width:1),@"builtIn":@(builtIn),@"main":@(main),@"x":@(bounds.origin.x),@"y":@(bounds.origin.y)}];
   if(mode)CGDisplayModeRelease(mode);
  }
  free(displays);
  NSMutableArray *programs=[NSMutableArray array];
  CFArrayRef windows=CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID);
  for(NSDictionary *window in (__bridge NSArray *)windows) {
   if([window[(id)kCGWindowLayer] intValue]!=0)continue;
   NSNumber *pid=window[(id)kCGWindowOwnerPID], *wid=window[(id)kCGWindowNumber];
   if(!pid||!wid||[pid intValue]==getpid())continue;
   CGRect bounds=CGRectZero;
   CGRectMakeWithDictionaryRepresentation((__bridge CFDictionaryRef)window[(id)kCGWindowBounds],&bounds);
   if(bounds.size.width<80||bounds.size.height<50)continue;
   NSString *name=window[(id)kCGWindowOwnerName]?:@"App";
   NSString *title=window[(id)kCGWindowName]?:@"";
   [programs addObject:@{@"id":[NSString stringWithFormat:@"window-%@",wid],@"kind":@"program",@"name":name,@"title":title,@"pid":pid,@"windowId":wid,@"width":@((int)bounds.size.width),@"height":@((int)bounds.size.height),@"x":@(bounds.origin.x),@"y":@(bounds.origin.y)}];
  }
  if(windows)CFRelease(windows);
  NSData *data=[NSJSONSerialization dataWithJSONObject:@{@"monitors":monitors,@"programs":programs} options:0 error:nil];
  if(!data)return NULL;
  char *result=malloc(data.length+1);if(!result)return NULL;memcpy(result,data.bytes,data.length);result[data.length]=0;return result;
 }
}
