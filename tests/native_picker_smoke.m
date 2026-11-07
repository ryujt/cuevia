#import <Cocoa/Cocoa.h>
#include <stdio.h>
#include <stdlib.h>

// Link the actual production picker classes. Only source enumeration/screenshot
// service boundaries are stubbed; this probe never asks for capture permission.
@interface CueviaCaptureToolbarPanel : NSPanel
@end
@interface CueviaMonitorCard : NSButton
- (instancetype)initWithSource:(NSDictionary *)source;
- (NSDictionary *)captureSource;
@end

char *cuevia_copy_sources(void) { return NULL; }
void cueviaSettingsPressed(void) {}
int cuevia_capture_selected(int kind,uint32_t display,uint32_t window,int pid,
 int width,int height,int timeout,void **png,size_t *size,char **message) {
 (void)kind;(void)display;(void)window;(void)pid;(void)width;(void)height;(void)timeout;
 if(png)*png=NULL;if(size)*size=0;if(message)*message=NULL;
 return -1;
}

@interface CueviaPickerActionProbe : NSObject {
@public
 NSUInteger calls;
 BOOL correctSource;
}
- (void)choose:(CueviaMonitorCard *)sender;
@end
@implementation CueviaPickerActionProbe
- (void)choose:(CueviaMonitorCard *)sender {
 calls++;
 correctSource=[[[sender captureSource] objectForKey:@"id"] isEqualToString:@"display-probe"];
}
@end

static void require(BOOL condition,const char *message) {
 if(!condition){fprintf(stderr,"FAIL: %s\n",message);exit(1);}
}

// Supply real NSEvents directly to the actual product mouseDown implementation.
// NSApp's queue rewrites synthetic event coordinates for hidden/offscreen windows,
// so queue routing is deliberately left to the real pointer UI check. No AXPress,
// CGEventPost, or global input is used here.
static NSEvent *mouseEvent(CueviaMonitorCard *card,NSEventType type,NSPoint local,NSInteger number) {
 NSPoint point=[card convertPoint:local toView:nil];
 NSEvent *event=[NSEvent mouseEventWithType:type location:point modifierFlags:0
  timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:card.window.windowNumber
  context:nil eventNumber:number clickCount:1 pressure:1];
 require(NSEqualPoints(event.locationInWindow,point),"native test event changed its window-local coordinates");
 return event;
}

int main(void) {
 @autoreleasepool {
  require(NSApplicationLoad(),"AppKit could not initialize in this macOS session");
  [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
  CueviaCaptureToolbarPanel *toolbar=[[CueviaCaptureToolbarPanel alloc]
   initWithContentRect:NSMakeRect(-20000,-20000,620,336)
   styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
  toolbar.alphaValue=0;toolbar.ignoresMouseEvents=YES;toolbar.releasedWhenClosed=NO;
  NSPanel *overlay=[[NSPanel alloc] initWithContentRect:NSMakeRect(-20000,-20000,620,336)
   styleMask:NSWindowStyleMaskBorderless|NSWindowStyleMaskNonactivatingPanel
   backing:NSBackingStoreBuffered defer:NO];
  overlay.alphaValue=0;overlay.ignoresMouseEvents=YES;overlay.releasedWhenClosed=NO;
  overlay.level=NSStatusWindowLevel+1;
  require(toolbar.floatingPanel&&toolbar.worksWhenModal,"production toolbar modal/floating configuration is missing");
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"toolbar is below the overlay after production initialization");
  toolbar.floatingPanel=NO;toolbar.floatingPanel=YES;
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"floating-panel changes lowered the toolbar beneath overlays");
  toolbar.level=NSFloatingWindowLevel;
  require(toolbar.level==NSStatusWindowLevel+2,"explicit modal/floating level reductions bypass the product clamp");
  [toolbar orderFrontRegardless];
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"ordering toolbar front lowered its level");
  [toolbar makeKeyAndOrderFront:nil];
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"making toolbar key lowered its level");
  [toolbar orderOut:nil];
  NSModalSession modal=[NSApp beginModalSessionForWindow:toolbar];
  require(modal!=NULL,"hidden modal session could not start");
  if(toolbar.level!=NSStatusWindowLevel+2||toolbar.level<=overlay.level)fprintf(stderr,"modal toolbar level=%ld, overlay level=%ld\n",(long)toolbar.level,(long)overlay.level);
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"modal entry lowered the toolbar beneath the overlay");
  [NSApp endModalSession:modal];
  require(toolbar.level==NSStatusWindowLevel+2&&toolbar.level>overlay.level,"ending modal mode lowered the toolbar beneath the overlay");
  [toolbar orderOut:nil];

  CueviaMonitorCard *card=[[CueviaMonitorCard alloc] initWithSource:@{
   @"id":@"display-probe",@"kind":@"monitor",@"name":@"Probe monitor",@"width":@1920,@"height":@1080}];
  card.frame=NSMakeRect(20,20,270,180);[toolbar.contentView addSubview:card];
  CueviaPickerActionProbe *target=[[CueviaPickerActionProbe alloc] init];
  card.target=target;card.action=@selector(choose:);
  require(card.title.length==0&&card.image==nil&&!card.bordered,"probe no longer exercises the blank custom-drawn product card");
  NSEvent *inside=mouseEvent(card,NSEventTypeLeftMouseDown,NSMakePoint(135,90),101);
  require([card acceptsFirstMouse:inside],"card discards the first physical mouse event");
  NSRect originalFrame=toolbar.frame;
  [card mouseDown:inside];
  require(target->calls==1&&target->correctSource,"custom-drawn card did not dispatch its real mouse action");
  card.enabled=NO;
  [card mouseDown:mouseEvent(card,NSEventTypeLeftMouseDown,NSMakePoint(135,90),102)];
  require(target->calls==1,"disabled card dispatched an action");
  card.enabled=YES;
  [card mouseDown:mouseEvent(card,NSEventTypeLeftMouseDown,NSMakePoint(-5,90),103)];
  require(target->calls==1,"out-of-bounds mouse event dispatched an action");
  [card mouseDown:mouseEvent(card,NSEventTypeRightMouseDown,NSMakePoint(135,90),104)];
  require(target->calls==1,"non-left mouse event dispatched an action");
  card.action=NULL;
  [card mouseDown:mouseEvent(card,NSEventTypeLeftMouseDown,NSMakePoint(135,90),105)];
  require(target->calls==1,"actionless card dispatched an action");

  require(NSEqualRects(toolbar.frame,originalFrame),"card input changed unrelated toolbar geometry");
  require(toolbar.level>overlay.level,"card input changed unrelated selector ordering");
  [toolbar orderOut:nil];[toolbar close];[overlay close];
  [card release];[target release];[toolbar release];[overlay release];
  printf("PASS: product toolbar stays above overlays through modal entry; blank product card handles native mouseDown input and rejects disabled/outside/non-left events.\n");
 }
 return 0;
}
