//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <Carbon/Carbon.h>
#import <QuartzCore/QuartzCore.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>
#include <signal.h>
#include <unistd.h>

extern char *cuevia_copy_sources(void);
extern void cueviaSettingsPressed(void);
int cuevia_request_capture(void);

@interface CueviaStatusMenuController : NSObject
- (void)capture:(id)sender;
- (void)settings:(id)sender;
- (void)quit:(id)sender;
@end
@implementation CueviaStatusMenuController
- (void)capture:(id)sender { (void)sender; cuevia_request_capture(); }
- (void)settings:(id)sender { (void)sender; cueviaSettingsPressed(); }
- (void)quit:(id)sender { (void)sender; if(NSApp.modalWindow)[NSApp stopModalWithCode:NSModalResponseCancel]; kill(getpid(),SIGTERM); }
@end

static NSStatusItem *cuevia_status_item;
static CueviaStatusMenuController *cuevia_status_controller;

int cuevia_dispatch_cocoa_event(void) {
 if(!pthread_main_np())return -20001;
 @autoreleasepool {
  NSApplication *app=[NSApplication sharedApplication];
  NSDate *deadline=[NSDate dateWithTimeIntervalSinceNow:0.15];
  NSEvent *event=[app nextEventMatchingMask:NSEventMaskAny untilDate:deadline inMode:NSDefaultRunLoopMode dequeue:YES];
  if(event)[app sendEvent:event];
  [app updateWindows];
  return 0;
 }
}

int cuevia_install_menu(void) {
 if(!pthread_main_np())return -20001;
 @autoreleasepool {
  if(cuevia_status_item)return 0;
  if(!NSApplicationLoad())return -2;
  NSApplication *app=[NSApplication sharedApplication];
  [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
  [app finishLaunching];
  cuevia_status_controller=[[CueviaStatusMenuController alloc] init];
  cuevia_status_item=[[[NSStatusBar systemStatusBar] statusItemWithLength:NSVariableStatusItemLength] retain];
  if(!cuevia_status_item)return -1;
  NSImage *image=[NSImage imageWithSystemSymbolName:@"square.and.pencil" accessibilityDescription:@"Cuevia"];
  if(!image)image=[NSImage imageNamed:NSImageNameActionTemplate];
  [image setSize:NSMakeSize(18,18)];
  [image setTemplate:YES];
  [[cuevia_status_item button] setImage:image];
  [[cuevia_status_item button] setToolTip:@"Cuevia · ⌘⌃S"];
  NSMenu *menu=[[NSMenu alloc] initWithTitle:@"Cuevia"];
  NSMenuItem *capture=[[NSMenuItem alloc] initWithTitle:@"화면 캡처…" action:@selector(capture:) keyEquivalent:@""];
  [capture setTarget:cuevia_status_controller];
  [menu addItem:capture];
  [capture release];
  NSMenuItem *settings=[[NSMenuItem alloc] initWithTitle:@"설정…" action:@selector(settings:) keyEquivalent:@""];
  [settings setTarget:cuevia_status_controller];
  [menu addItem:settings];
  [settings release];
  [menu addItem:[NSMenuItem separatorItem]];
  NSMenuItem *quit=[[NSMenuItem alloc] initWithTitle:@"종료" action:@selector(quit:) keyEquivalent:@""];
  [quit setTarget:cuevia_status_controller];
  [menu addItem:quit];
  [quit release];
  [cuevia_status_item setMenu:menu];
  [menu release];
  return 0;
 }
}


@class CueviaCapturePickerController;

@interface CueviaCaptureToolbarPanel : NSPanel
@property(nonatomic,assign) CueviaCapturePickerController *picker;
@end

@interface CueviaCaptureOverlayView : NSView
@property(nonatomic,assign) CueviaCapturePickerController *picker;
@end

@interface CueviaMonitorCard : NSButton {
 NSDictionary *source;
 NSImage *preview;
 BOOL loading;
 BOOL hovering;
}
- (instancetype)initWithSource:(NSDictionary *)value;
- (NSDictionary *)captureSource;
- (void)setPreview:(NSImage *)image;
@end

@interface CueviaCapturePickerController : NSObject {
@public
 BOOL active;
 NSInteger mode;
 CueviaCaptureToolbarPanel *toolbar;
 NSVisualEffectView *surface;
 NSSegmentedControl *tabs;
 NSTextField *titleLabel;
 NSTextField *hint;
 NSButton *cancelButton;
 NSScrollView *monitorScroll;
 NSMutableArray *overlays;
 NSMutableArray *cards;
 NSTimer *hoverTimer;
 NSDictionary *hoverSource;
 NSDictionary *chosenSource;
 NSRect hoverRect;
 CGFloat primaryTop;
 BOOL previewsStarted;
}
- (instancetype)initWithSources:(NSDictionary *)sources;
- (void)changeMode:(id)sender;
- (void)cancel:(id)sender;
- (void)captureWindowAtPointer;
- (void)captureMonitor:(CueviaMonitorCard *)sender;
- (void)refreshHover:(id)sender;
- (void)startPreviews;
- (void)dismiss;
@end

static NSDictionary *cuevia_picker_diagnostic_point(NSView *view,CGFloat primaryTop) {
 NSPoint local=NSMakePoint(NSMidX(view.bounds),NSMidY(view.bounds));
 NSPoint global=[view.window convertPointToScreen:[view convertPoint:local toView:nil]];
 return @{@"x":@(global.x),@"y":@(primaryTop-global.y)};
}

@implementation CueviaCaptureToolbarPanel
- (void)setLevel:(NSWindowLevel)level {
 // AppKit also assigns NSModalPanelWindowLevel when a modal session begins.
 // This toolbar must stay above its full-screen capture overlays throughout.
 [super setLevel:MAX(level,NSStatusWindowLevel+2)];
}
- (instancetype)initWithContentRect:(NSRect)rect styleMask:(NSWindowStyleMask)style backing:(NSBackingStoreType)backing defer:(BOOL)defer {
 if((self=[super initWithContentRect:rect styleMask:style backing:backing defer:defer])) {
  self.worksWhenModal=YES;
  // NSPanel resets its level to NSFloatingWindowLevel when this is enabled.
  self.floatingPanel=YES;
  self.level=NSStatusWindowLevel+2;
 }
 return self;
}
- (void)orderFrontRegardless { self.level=NSStatusWindowLevel+2;[super orderFrontRegardless]; }
- (void)makeKeyAndOrderFront:(id)sender { self.level=NSStatusWindowLevel+2;[super makeKeyAndOrderFront:sender]; }
- (BOOL)canBecomeKeyWindow { return YES; }
- (BOOL)canBecomeMainWindow { return NO; }
- (NSString *)accessibilityHelp {
 const char *flag=getenv("CUEVIA_UI_DIAGNOSTICS");
 if(!flag||strcmp(flag,"1")!=0||!self.picker)return [super accessibilityHelp];
 CueviaCapturePickerController *picker=self.picker;
 NSRect frame=self.frame;
 NSPoint appLocal=NSMakePoint(NSMinX(picker->tabs.bounds)+picker->tabs.bounds.size.width/4,NSMidY(picker->tabs.bounds));
 NSPoint monitorLocal=NSMakePoint(NSMinX(picker->tabs.bounds)+picker->tabs.bounds.size.width*3/4,NSMidY(picker->tabs.bounds));
 NSPoint appGlobal=[self convertPointToScreen:[picker->tabs convertPoint:appLocal toView:nil]];
 NSPoint monitorGlobal=[self convertPointToScreen:[picker->tabs convertPoint:monitorLocal toView:nil]];
 NSMutableArray *cardCenters=[NSMutableArray array];
 for(CueviaMonitorCard *card in picker->cards) {
  [cardCenters addObject:@{@"id":[card captureSource][@"id"]?:@"",@"center":cuevia_picker_diagnostic_point(card,picker->primaryTop),@"visible":@(!picker->monitorScroll.hidden&&!NSIsEmptyRect(card.visibleRect))}];
 }
 NSDictionary *diagnostic=@{@"coordinateSpace":@"QuartzGlobalPoints",@"toolbarRect":@{@"x":@(frame.origin.x),@"y":@(picker->primaryTop-NSMaxY(frame)),@"width":@(frame.size.width),@"height":@(frame.size.height)},@"toolbarLevel":@(self.level),@"appTab":@{@"x":@(appGlobal.x),@"y":@(picker->primaryTop-appGlobal.y)},@"monitorTab":@{@"x":@(monitorGlobal.x),@"y":@(picker->primaryTop-monitorGlobal.y)},@"cancel":cuevia_picker_diagnostic_point(picker->cancelButton,picker->primaryTop),@"cards":cardCenters};
 NSData *data=[NSJSONSerialization dataWithJSONObject:diagnostic options:0 error:nil];
 return data?[[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] autorelease]:[super accessibilityHelp];
}
- (void)cancelOperation:(id)sender { [self.picker cancel:sender]; }
- (void)keyDown:(NSEvent *)event {
 if(event.keyCode==53){[self.picker cancel:nil];return;}
 [super keyDown:event];
}
@end

@implementation CueviaCaptureOverlayView
- (BOOL)isOpaque { return NO; }
- (BOOL)acceptsFirstMouse:(NSEvent *)event { (void)event;return YES; }
- (void)resetCursorRects { [self addCursorRect:self.bounds cursor:NSCursor.crosshairCursor]; }
- (void)updateTrackingAreas {
 for(NSTrackingArea *area in [[self.trackingAreas copy] autorelease])[self removeTrackingArea:area];
 NSTrackingArea *area=[[NSTrackingArea alloc] initWithRect:NSZeroRect options:NSTrackingMouseMoved|NSTrackingMouseEnteredAndExited|NSTrackingActiveAlways|NSTrackingInVisibleRect owner:self userInfo:nil];
 [self addTrackingArea:area];[area release];[super updateTrackingAreas];
}
- (void)mouseMoved:(NSEvent *)event { (void)event;[self.picker refreshHover:nil]; }
- (void)mouseEntered:(NSEvent *)event { (void)event;[self.picker refreshHover:nil]; }
- (void)mouseDown:(NSEvent *)event { (void)event;[self.picker captureWindowAtPointer]; }
- (void)drawRect:(NSRect)dirty {
 (void)dirty;
 [[NSColor colorWithWhite:0 alpha:0.07] setFill];NSRectFillUsingOperation(self.bounds,NSCompositingOperationCopy);
 CueviaCapturePickerController *picker=self.picker;
 if(!picker->active||picker->mode!=0||!picker->hoverSource)return;
 NSRect global=picker->hoverRect;
 NSRect intersection=NSIntersectionRect(global,self.window.frame);
 if(NSIsEmptyRect(intersection))return;
 NSRect local=NSOffsetRect(global,-NSMinX(self.window.frame),-NSMinY(self.window.frame));
 NSBezierPath *outline=[NSBezierPath bezierPathWithRoundedRect:NSInsetRect(local,1.5,1.5) xRadius:8 yRadius:8];
 [[NSColor colorWithCalibratedRed:0.24 green:0.56 blue:1 alpha:0.16] setFill];[outline fill];
 [[NSColor colorWithCalibratedRed:0.32 green:0.66 blue:1 alpha:0.98] setStroke];[outline setLineWidth:3];[outline stroke];
 NSRect visible=NSOffsetRect(intersection,-NSMinX(self.window.frame),-NSMinY(self.window.frame));
 if(visible.size.width<120||visible.size.height<60)return;
 NSString *label=[NSString stringWithFormat:@"%@ · PID %@",picker->hoverSource[@"name"],picker->hoverSource[@"pid"]];
 NSDictionary *attributes=@{NSFontAttributeName:[NSFont systemFontOfSize:11 weight:NSFontWeightSemibold],NSForegroundColorAttributeName:NSColor.whiteColor};
 CGFloat badgeWidth=MIN([label sizeWithAttributes:attributes].width+24,visible.size.width-24);
 NSRect badge=NSMakeRect(NSMinX(visible)+12,NSMaxY(visible)-40,badgeWidth,27);
 [[NSColor colorWithWhite:0.06 alpha:0.88] setFill];[[NSBezierPath bezierPathWithRoundedRect:badge xRadius:8 yRadius:8] fill];
 NSMutableParagraphStyle *paragraph=[[[NSMutableParagraphStyle alloc] init] autorelease];paragraph.lineBreakMode=NSLineBreakByTruncatingTail;
 NSMutableDictionary *labelAttributes=[attributes mutableCopy];labelAttributes[NSParagraphStyleAttributeName]=paragraph;
 [label drawInRect:NSInsetRect(badge,12,6) withAttributes:labelAttributes];[labelAttributes release];
}
@end

@implementation CueviaMonitorCard
- (instancetype)initWithSource:(NSDictionary *)value {
 if((self=[super initWithFrame:NSZeroRect])) {
  source=[value copy];loading=YES;
  self.bordered=NO;self.buttonType=NSButtonTypeMomentaryPushIn;self.focusRingType=NSFocusRingTypeExterior;
  self.title=@"";
  self.toolTip=[NSString stringWithFormat:@"%@ · %@ × %@",source[@"name"],source[@"width"],source[@"height"]];
  [self setAccessibilityLabel:[NSString stringWithFormat:@"%@, %@ × %@, 클릭하여 캡처",source[@"name"],source[@"width"],source[@"height"]]];
 }
 return self;
}
- (BOOL)acceptsFirstMouse:(NSEvent *)event { (void)event;return YES; }
- (void)mouseDown:(NSEvent *)event {
 if(!self.enabled||event.type!=NSEventTypeLeftMouseDown||!self.action)return;
 NSPoint point=[self convertPoint:event.locationInWindow fromView:nil];
 if(!NSPointInRect(point,self.bounds))return;
 // The native cell has no title/image: the full custom-drawn card owns hits.
 [self sendAction:self.action to:self.target];
}
- (NSDictionary *)captureSource { return source; }
- (void)setPreview:(NSImage *)image { [preview release];preview=[image retain];loading=NO;[self setNeedsDisplay:YES]; }
- (void)updateTrackingAreas {
 for(NSTrackingArea *area in [[self.trackingAreas copy] autorelease])[self removeTrackingArea:area];
 NSTrackingArea *area=[[NSTrackingArea alloc] initWithRect:NSZeroRect options:NSTrackingMouseEnteredAndExited|NSTrackingActiveAlways|NSTrackingInVisibleRect owner:self userInfo:nil];
 [self addTrackingArea:area];[area release];[super updateTrackingAreas];
}
- (void)mouseEntered:(NSEvent *)event { (void)event;hovering=YES;[self setNeedsDisplay:YES]; }
- (void)mouseExited:(NSEvent *)event { (void)event;hovering=NO;[self setNeedsDisplay:YES]; }
- (void)drawRect:(NSRect)dirty {
 (void)dirty;
 NSRect box=NSInsetRect(self.bounds,1,1);
 NSBezierPath *background=[NSBezierPath bezierPathWithRoundedRect:box xRadius:12 yRadius:12];
 [[NSColor colorWithWhite:1 alpha:hovering?0.14:0.075] setFill];[background fill];
 BOOL focused=self.window.firstResponder==self;
 [(focused?NSColor.keyboardFocusIndicatorColor:[NSColor colorWithWhite:1 alpha:hovering?0.42:0.14]) setStroke];[background setLineWidth:focused?2:1];[background stroke];
 NSRect imageRect=NSMakeRect(12,53,box.size.width-24,box.size.height-65);
 [[NSColor colorWithWhite:0 alpha:0.35] setFill];[[NSBezierPath bezierPathWithRoundedRect:imageRect xRadius:7 yRadius:7] fill];
 if(preview) {
  NSSize size=preview.size;CGFloat scale=MIN(imageRect.size.width/MAX(1,size.width),imageRect.size.height/MAX(1,size.height));
  NSRect image=NSMakeRect(NSMidX(imageRect)-size.width*scale/2,NSMidY(imageRect)-size.height*scale/2,size.width*scale,size.height*scale);
  [preview drawInRect:image fromRect:NSZeroRect operation:NSCompositingOperationSourceOver fraction:1 respectFlipped:NO hints:nil];
 } else {
  NSString *placeholder=loading?@"미리보기 불러오는 중…":@"미리보기 없이 캡처할 수 있어요";
  NSDictionary *attributes=@{NSFontAttributeName:[NSFont systemFontOfSize:10],NSForegroundColorAttributeName:[NSColor colorWithWhite:1 alpha:0.55]};
  NSSize size=[placeholder sizeWithAttributes:attributes];
  [placeholder drawAtPoint:NSMakePoint(NSMidX(imageRect)-size.width/2,NSMidY(imageRect)-size.height/2) withAttributes:attributes];
 }
 NSMutableParagraphStyle *paragraph=[[[NSMutableParagraphStyle alloc] init] autorelease];paragraph.lineBreakMode=NSLineBreakByTruncatingTail;
 NSDictionary *nameAttributes=@{NSFontAttributeName:[NSFont systemFontOfSize:11 weight:NSFontWeightSemibold],NSForegroundColorAttributeName:NSColor.whiteColor,NSParagraphStyleAttributeName:paragraph};
 NSString *name=[NSString stringWithFormat:@"%@%@",source[@"name"], [source[@"main"] boolValue]?@" · 주 모니터":@""];
 [name drawInRect:NSMakeRect(14,29,box.size.width-28,17) withAttributes:nameAttributes];
 NSString *resolution=[NSString stringWithFormat:@"%@ × %@",source[@"width"],source[@"height"]];
 [resolution drawInRect:NSMakeRect(14,12,box.size.width-28,15) withAttributes:@{NSFontAttributeName:[NSFont monospacedDigitSystemFontOfSize:10 weight:NSFontWeightRegular],NSForegroundColorAttributeName:[NSColor colorWithWhite:1 alpha:0.6]}];
}
- (void)dealloc { [source release];[preview release];[super dealloc]; }
@end

extern int cuevia_capture_selected(int kind,uint32_t display_id,uint32_t window_id,int pid,int width,int height,int timeout_ms,void **png,size_t *png_size,char **message);

static NSTextField *cuevia_picker_label(NSString *text,CGFloat size,NSFontWeight weight) {
 NSTextField *field=[NSTextField labelWithString:text];field.font=[NSFont systemFontOfSize:size weight:weight];field.textColor=NSColor.whiteColor;
 field.lineBreakMode=NSLineBreakByTruncatingTail;return field;
}

@implementation CueviaCapturePickerController
- (instancetype)initWithSources:(NSDictionary *)sources {
 if(!(self=[super init]))return nil;
 active=YES;mode=0;primaryTop=0;overlays=[[NSMutableArray alloc] init];cards=[[NSMutableArray alloc] init];
 NSScreen *target=nil;NSPoint pointer=NSEvent.mouseLocation;
 for(NSScreen *screen in NSScreen.screens) {
  if([screen.deviceDescription[@"NSScreenNumber"] unsignedIntValue]==CGMainDisplayID())primaryTop=NSMaxY(screen.frame);
  if(NSPointInRect(pointer,screen.frame))target=screen;
 }
 if(!target)target=NSScreen.screens.firstObject;
 if(!target){[self release];return nil;}
 if(primaryTop==0)primaryTop=NSMaxY(NSScreen.screens.firstObject.frame);
 toolbar=[[CueviaCaptureToolbarPanel alloc] initWithContentRect:NSMakeRect(0,0,620,132) styleMask:NSWindowStyleMaskBorderless backing:NSBackingStoreBuffered defer:NO];
 toolbar.picker=self;toolbar.opaque=NO;toolbar.backgroundColor=NSColor.clearColor;toolbar.hasShadow=YES;toolbar.releasedWhenClosed=NO;
 toolbar.collectionBehavior=NSWindowCollectionBehaviorCanJoinAllSpaces|NSWindowCollectionBehaviorFullScreenAuxiliary|NSWindowCollectionBehaviorIgnoresCycle;
 toolbar.appearance=[NSAppearance appearanceNamed:NSAppearanceNameDarkAqua];
 surface=[[NSVisualEffectView alloc] initWithFrame:NSMakeRect(0,0,620,132)];surface.material=NSVisualEffectMaterialHUDWindow;surface.blendingMode=NSVisualEffectBlendingModeBehindWindow;surface.state=NSVisualEffectStateActive;
 surface.wantsLayer=YES;surface.layer.cornerRadius=15;surface.layer.masksToBounds=YES;surface.autoresizingMask=NSViewWidthSizable|NSViewHeightSizable;
 toolbar.contentView=surface;
 titleLabel=[cuevia_picker_label(@"화면 캡처",12,NSFontWeightSemibold) retain];[surface addSubview:titleLabel];
 cancelButton=[[NSButton buttonWithTitle:@"취소" target:self action:@selector(cancel:)] retain];cancelButton.bezelStyle=NSBezelStyleInline;cancelButton.font=[NSFont systemFontOfSize:11];cancelButton.keyEquivalent=@"\033";[surface addSubview:cancelButton];
 tabs=[[NSSegmentedControl alloc] initWithFrame:NSZeroRect];tabs.segmentCount=2;[tabs setLabel:@"어플리케이션 캡처" forSegment:0];[tabs setLabel:@"모니터 캡처" forSegment:1];tabs.selectedSegment=0;tabs.trackingMode=NSSegmentSwitchTrackingSelectOne;
 [tabs setWidth:289 forSegment:0];[tabs setWidth:289 forSegment:1];tabs.target=self;tabs.action=@selector(changeMode:);tabs.font=[NSFont systemFontOfSize:12];[tabs setAccessibilityLabel:@"캡처 방식"];[surface addSubview:tabs];
 hint=[cuevia_picker_label(@"창 위로 마우스를 옮긴 뒤 클릭하세요. Esc로 취소할 수 있어요.",11,NSFontWeightRegular) retain];hint.textColor=[NSColor colorWithWhite:1 alpha:0.7];[surface addSubview:hint];
 monitorScroll=[[NSScrollView alloc] initWithFrame:NSZeroRect];monitorScroll.drawsBackground=NO;monitorScroll.hasVerticalScroller=NO;monitorScroll.hasHorizontalScroller=YES;monitorScroll.autohidesScrollers=YES;monitorScroll.borderType=NSNoBorder;monitorScroll.hidden=YES;
 NSArray *monitors=[sources[@"monitors"] isKindOfClass:NSArray.class]?sources[@"monitors"]:@[];
 CGFloat cardWidth=270,gap=12,documentWidth=MAX(584,monitors.count*(cardWidth+gap)-gap);
 NSView *document=[[NSView alloc] initWithFrame:NSMakeRect(0,0,documentWidth,188)];
 for(NSUInteger index=0;index<monitors.count;index++) {
  CueviaMonitorCard *card=[[CueviaMonitorCard alloc] initWithSource:monitors[index]];
  card.frame=NSMakeRect(monitors.count==1?(584-cardWidth)/2:index*(cardWidth+gap),4,cardWidth,180);
  card.target=self;card.action=@selector(captureMonitor:);[cards addObject:card];[document addSubview:card];[card release];
 }
 if(monitors.count==0){NSTextField *empty=cuevia_picker_label(@"사용할 수 있는 모니터가 없어요",12,NSFontWeightRegular);empty.frame=NSMakeRect(20,80,544,24);empty.alignment=NSTextAlignmentCenter;[document addSubview:empty];}
 monitorScroll.documentView=document;[document release];[surface addSubview:monitorScroll];
 for(NSScreen *screen in NSScreen.screens) {
  NSPanel *overlay=[[NSPanel alloc] initWithContentRect:screen.frame styleMask:NSWindowStyleMaskBorderless|NSWindowStyleMaskNonactivatingPanel backing:NSBackingStoreBuffered defer:NO];
  overlay.opaque=NO;overlay.backgroundColor=NSColor.clearColor;overlay.hasShadow=NO;overlay.releasedWhenClosed=NO;overlay.level=NSStatusWindowLevel+1;overlay.worksWhenModal=YES;overlay.acceptsMouseMovedEvents=YES;
  overlay.collectionBehavior=NSWindowCollectionBehaviorCanJoinAllSpaces|NSWindowCollectionBehaviorFullScreenAuxiliary|NSWindowCollectionBehaviorIgnoresCycle;
  CueviaCaptureOverlayView *view=[[CueviaCaptureOverlayView alloc] initWithFrame:NSMakeRect(0,0,screen.frame.size.width,screen.frame.size.height)];view.picker=self;overlay.contentView=view;[view release];[overlays addObject:overlay];[overlay release];
 }
 NSRect visible=target.visibleFrame;
 [toolbar setFrame:NSMakeRect(NSMidX(visible)-310,NSMaxY(visible)-152,620,132) display:NO];
 [self changeMode:nil];
 hoverTimer=[[NSTimer timerWithTimeInterval:1.0/30 target:self selector:@selector(refreshHover:) userInfo:nil repeats:YES] retain];hoverTimer.tolerance=0.008;
 [[NSRunLoop mainRunLoop] addTimer:hoverTimer forMode:NSRunLoopCommonModes];
 [[NSRunLoop mainRunLoop] addTimer:hoverTimer forMode:NSModalPanelRunLoopMode];
 return self;
}
- (void)changeMode:(id)sender {
 (void)sender;if(!active)return;mode=tabs.selectedSegment;
 [hoverSource release];hoverSource=nil;hoverRect=NSZeroRect;
 CGFloat height=mode==0?132:336;
 NSScreen *screen=toolbar.screen?:NSScreen.mainScreen?:NSScreen.screens.firstObject;
 NSRect visible=screen.visibleFrame;
 NSUInteger visibleCards=MIN((NSUInteger)3,cards.count);
 CGFloat wantedWidth=mode==0?620:MAX(620,visibleCards*270+MAX(0,(NSInteger)visibleCards-1)*12+36);
 CGFloat width=MIN(wantedWidth,MAX(320,visible.size.width-48)),innerWidth=width-36;
 NSRect frame=toolbar.frame;
 frame.origin.x=MAX(NSMinX(visible)+24,MIN(NSMidX(visible)-width/2,NSMaxX(visible)-24-width));
 frame.origin.y=MAX(NSMinY(visible)+24,MIN(NSMaxY(frame),NSMaxY(visible)-24)-height);
 frame.size=NSMakeSize(width,height);[toolbar setFrame:frame display:YES];
 titleLabel.frame=NSMakeRect(20,height-31,width-150,20);cancelButton.frame=NSMakeRect(width-64,height-33,44,24);
 tabs.frame=NSMakeRect(18,height-76,innerWidth,30);
 [tabs setWidth:(innerWidth-6)/2 forSegment:0];[tabs setWidth:(innerWidth-6)/2 forSegment:1];
 hint.frame=NSMakeRect(20,height-111,width-40,21);
 monitorScroll.frame=NSMakeRect(18,12,innerWidth,188);monitorScroll.hidden=mode==0;
 if(mode==1) {
  CGFloat cardWidth=visibleCards?MIN(270,MAX(200,(innerWidth-(visibleCards-1)*12)/visibleCards)):270;
  CGFloat total=cards.count?cards.count*cardWidth+(cards.count-1)*12:0,documentWidth=MAX(innerWidth,total);
  monitorScroll.documentView.frame=NSMakeRect(0,0,documentWidth,188);
  for(NSUInteger index=0;index<cards.count;index++)[(CueviaMonitorCard *)cards[index] setFrame:NSMakeRect((documentWidth-total)/2+index*(cardWidth+12),4,cardWidth,180)];
  if(cards.count==0&&monitorScroll.documentView.subviews.count)[monitorScroll.documentView.subviews.firstObject setFrame:NSMakeRect(20,80,documentWidth-40,24)];
  monitorScroll.hasHorizontalScroller=total>innerWidth+0.5;
 }
 hint.stringValue=mode==0?@"창 위로 마우스를 옮긴 뒤 클릭하세요. Esc로 취소할 수 있어요.":@"캡처할 모니터를 클릭하세요. Esc로 취소할 수 있어요.";
 hint.toolTip=nil;
 for(NSPanel *overlay in overlays){if(mode==0)[overlay orderFrontRegardless];else[overlay orderOut:nil];[overlay.contentView setNeedsDisplay:YES];}
 [toolbar orderFrontRegardless];
 if(mode==1)[self startPreviews];else[self refreshHover:nil];
}
- (void)refreshHover:(id)sender {
 (void)sender;if(!active||mode!=0)return;
 NSPoint pointer=NSEvent.mouseLocation;
 NSDictionary *selected=nil;NSRect rectangle=NSZeroRect;
 if(!NSPointInRect(pointer,toolbar.frame)) {
  CGPoint quartz=CGPointMake(pointer.x,primaryTop-pointer.y);
  CFArrayRef windows=CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID);
  for(NSDictionary *window in (__bridge NSArray *)windows) {
   NSNumber *pid=window[(id)kCGWindowOwnerPID],*wid=window[(id)kCGWindowNumber];
   if([window[(id)kCGWindowLayer] intValue]!=0||!pid||!wid||pid.intValue==getpid())continue;
   if(window[(id)kCGWindowAlpha]&&[window[(id)kCGWindowAlpha] doubleValue]<=0)continue;
   CGRect bounds=CGRectZero;
   if(!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)window[(id)kCGWindowBounds],&bounds)||bounds.size.width<80||bounds.size.height<50||!CGRectContainsPoint(bounds,quartz))continue;
   NSString *name=window[(id)kCGWindowOwnerName]?:@"어플리케이션";
   selected=@{@"id":[NSString stringWithFormat:@"window-%@",wid],@"kind":@"program",@"name":name,@"title":window[(id)kCGWindowName]?:@"",@"pid":pid,@"windowId":wid,@"width":@((int)bounds.size.width),@"height":@((int)bounds.size.height),@"x":@(bounds.origin.x),@"y":@(bounds.origin.y)};
   rectangle=NSMakeRect(bounds.origin.x,primaryTop-CGRectGetMaxY(bounds),bounds.size.width,bounds.size.height);break;
  }
  if(windows)CFRelease(windows);
 }
 BOOL changed=![(hoverSource[@"id"]?:@"") isEqualToString:(selected[@"id"]?:@"")]||!NSEqualRects(hoverRect,rectangle);
 if(changed){[hoverSource release];hoverSource=[selected copy];hoverRect=rectangle;for(NSPanel *overlay in overlays)[overlay.contentView setNeedsDisplay:YES];}
 hint.stringValue=hoverSource?[NSString stringWithFormat:@"%@ · PID %@ — 클릭하여 이 창 캡처",hoverSource[@"name"],hoverSource[@"pid"]]:@"창 위로 마우스를 옮긴 뒤 클릭하세요. Esc로 취소할 수 있어요.";
 hint.toolTip=hoverSource[@"title"];
}
- (void)captureWindowAtPointer {
 if(!active||mode!=0)return;[self refreshHover:nil];if(!hoverSource)return;
 [chosenSource release];chosenSource=[hoverSource copy];[NSApp stopModalWithCode:NSModalResponseOK];
}
- (void)captureMonitor:(CueviaMonitorCard *)sender {
 if(!active||mode!=1)return;[chosenSource release];chosenSource=[[sender captureSource] copy];[NSApp stopModalWithCode:NSModalResponseOK];
}
- (void)cancel:(id)sender { (void)sender;if(active)[NSApp stopModalWithCode:NSModalResponseCancel]; }
- (void)startPreviews {
 if(previewsStarted)return;previewsStarted=YES;
 for(CueviaMonitorCard *card in cards) {
  NSDictionary *source=[card captureSource];
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY,0),^{
   @autoreleasepool {
    void *png=NULL;size_t size=0;char *error=NULL;
    int status=cuevia_capture_selected(1,[source[@"displayId"] unsignedIntValue],0,0,[source[@"width"] intValue],[source[@"height"] intValue],5000,&png,&size,&error);
    NSData *data=status==0&&png&&size?[NSData dataWithBytes:png length:size]:nil;
    free(png);free(error);
    dispatch_async(dispatch_get_main_queue(),^{
     if(!self->active)return;
     NSImage *image=data?[[NSImage alloc] initWithData:data]:nil;[card setPreview:image];[image release];
    });
   }
  });
 }
}
- (void)dismiss {
 active=NO;[hoverTimer invalidate];[hoverTimer release];hoverTimer=nil;
 for(NSPanel *overlay in overlays){[overlay orderOut:nil];[overlay close];}
 [toolbar orderOut:nil];[toolbar close];
}
- (void)dealloc {
 [hoverTimer invalidate];[hoverTimer release];[overlays release];[cards release];
 [hoverSource release];[chosenSource release];[titleLabel release];[hint release];[tabs release];[cancelButton release];[monitorScroll release];[surface release];[toolbar release];[super dealloc];
}
@end

// Status: 0 success, 1 user cancellation, 2 unavailable, 3 wrong thread.
char *cuevia_choose_source(int *status) {
 if(status)*status=2;if(!pthread_main_np()){if(status)*status=3;return NULL;}
 @autoreleasepool {
  NSRunningApplication *previous=[[NSWorkspace sharedWorkspace] frontmostApplication];
  char *raw=cuevia_copy_sources();if(!raw)return NULL;
  NSData *data=[NSData dataWithBytes:raw length:strlen(raw)];free(raw);
  NSDictionary *sources=[NSJSONSerialization JSONObjectWithData:data options:0 error:nil];
  if(![sources isKindOfClass:NSDictionary.class])return NULL;
  if(!NSApplicationLoad())return NULL;
  NSApplication *app=[NSApplication sharedApplication];[app setActivationPolicy:NSApplicationActivationPolicyAccessory];
  CueviaCapturePickerController *picker=[[CueviaCapturePickerController alloc] initWithSources:sources];if(!picker)return NULL;
  [app activateIgnoringOtherApps:YES];
  [picker->toolbar makeKeyAndOrderFront:nil];[picker->toolbar makeFirstResponder:picker->tabs];
  [NSApp runModalForWindow:picker->toolbar];
  NSData *chosen=picker->chosenSource?[NSJSONSerialization dataWithJSONObject:picker->chosenSource options:0 error:nil]:nil;
  char *result=NULL;if(chosen){result=malloc(chosen.length+1);if(result){memcpy(result,chosen.bytes,chosen.length);result[chosen.length]=0;}}
  [picker dismiss];[app hide:nil];
  if(previous&&previous.processIdentifier!=getpid())[previous activateWithOptions:NSApplicationActivateIgnoringOtherApps];
  [[NSRunLoop currentRunLoop] runUntilDate:[NSDate dateWithTimeIntervalSinceNow:0.15]];
  [picker release];if(status)*status=result?0:(chosen?2:1);return result;
 }
}

int cuevia_request_capture(void) {
 EventRef event=NULL;
 OSStatus status=CreateEvent(NULL,kEventClassKeyboard,kEventHotKeyPressed,GetCurrentEventTime(),kEventAttributeUserEvent,&event);
 if(status!=noErr)return (int)status;
 EventHotKeyID identifier={'Cuvi',1};
 status=SetEventParameter(event,kEventParamDirectObject,typeEventHotKeyID,sizeof(identifier),&identifier);
 if(status==noErr)status=PostEventToQueue(GetMainEventQueue(),event,kEventPriorityStandard);
 ReleaseEvent(event);
 return (int)status;
}
