//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <ImageIO/ImageIO.h>
#import <CoreVideo/CoreVideo.h>
#include <dispatch/dispatch.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

// The framework retains copied completion blocks and their captured result
// object. A timed-out caller abandons this object, then releases its ownership;
// late callbacks remain safe and can never touch an output path.
@interface CueviaScreenshotResult : NSObject {
@public
 NSLock *lock;
 dispatch_semaphore_t completion;
 BOOL finished;
 BOOL abandoned;
 NSData *png;
 NSString *message;
 NSInteger code;
}
- (BOOL)isAbandoned;
- (void)completePNG:(NSData *)data error:(NSError *)error;
- (void)abandon;
@end

@implementation CueviaScreenshotResult
- (instancetype)init {
 if((self=[super init])){lock=[[NSLock alloc] init];completion=dispatch_semaphore_create(0);}
 return self;
}
- (BOOL)isAbandoned { [lock lock];BOOL value=abandoned;[lock unlock];return value; }
- (void)completePNG:(NSData *)data error:(NSError *)error {
 [lock lock];
 if(!abandoned&&!finished){
  finished=YES;png=[data retain];message=[[error localizedDescription] copy];code=error?error.code:0;
  dispatch_semaphore_signal(completion);
 }
 [lock unlock];
}
- (void)abandon { [lock lock];abandoned=YES;[lock unlock]; }
- (void)dealloc {
 [png release];[message release];[lock release];dispatch_release(completion);[super dealloc];
}
@end

static NSError *cuevia_screenshot_error(NSInteger code, NSString *message) {
 return [NSError errorWithDomain:@"CueviaScreenCapture" code:code userInfo:@{NSLocalizedDescriptionKey:message}];
}

int cuevia_capture_selected(int kind,uint32_t display_id,uint32_t window_id,int pid,
 int width,int height,int timeout_ms,void **output,size_t *output_size,char **failure) {
 if(output)*output=NULL;if(output_size)*output_size=0;if(failure)*failure=NULL;
 if(!output||!output_size||!failure)return -3901;
 if(@available(macOS 14.0,*)) {
  @autoreleasepool {
   CueviaScreenshotResult *result=[[CueviaScreenshotResult alloc] init];
   [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content,NSError *error){
    @autoreleasepool {
     if([result isAbandoned])return;
     if(error||!content){[result completePNG:nil error:error?:cuevia_screenshot_error(-3901,@"화면 목록을 읽지 못했어요")];return;}
     SCContentFilter *filter=nil;
     size_t pixelsWide=0,pixelsHigh=0;
     if(kind==1) {
      for(SCDisplay *display in content.displays)if(display.displayID==display_id){
       // Capture the user's display without Cuevia's selector/hover overlays.
       // Application exclusion also covers panels created after enumeration.
       SCRunningApplication *ownApplication=nil;
       for(SCRunningApplication *application in content.applications)if(application.processID==getpid()){ownApplication=application;break;}
       if(ownApplication)filter=[[SCContentFilter alloc] initWithDisplay:display excludingApplications:@[ownApplication] exceptingWindows:@[]];
       else {
        NSMutableArray *ownWindows=[NSMutableArray array];
        for(SCWindow *window in content.windows)if(window.owningApplication.processID==getpid())[ownWindows addObject:window];
        filter=[[SCContentFilter alloc] initWithDisplay:display excludingWindows:ownWindows];
       }
       pixelsWide=width>0?(size_t)width:0;pixelsHigh=height>0?(size_t)height:0;break;
      }
     } else if(kind==2) {
      for(SCWindow *window in content.windows)if(window.windowID==window_id&&window.owningApplication.processID==pid){
       filter=[[SCContentFilter alloc] initWithDesktopIndependentWindow:window];
       double scale=filter.pointPixelScale;
       CGRect rect=filter.contentRect;
       double scaledWidth=ceil(rect.size.width*scale),scaledHeight=ceil(rect.size.height*scale);
       if(isfinite(scaledWidth)&&isfinite(scaledHeight)&&scaledWidth>0&&scaledHeight>0&&scaledWidth<=32768&&scaledHeight<=32768){pixelsWide=(size_t)scaledWidth;pixelsHigh=(size_t)scaledHeight;}
       break;
      }
     }
     if(!filter){[result completePNG:nil error:cuevia_screenshot_error(-3901,@"선택한 프로그램이나 모니터가 더 이상 없어요. 다시 선택해주세요")];return;}
     if(pixelsWide==0||pixelsHigh==0||pixelsWide>32768||pixelsHigh>32768||pixelsWide*pixelsHigh>40000000){
      [filter release];[result completePNG:nil error:cuevia_screenshot_error(-3901,@"캡처할 화면 크기를 확인하지 못했어요")];return;
     }
     SCStreamConfiguration *config=[[SCStreamConfiguration alloc] init];
     config.width=pixelsWide;config.height=pixelsHigh;config.showsCursor=NO;
     config.scalesToFit=NO;config.preservesAspectRatio=YES;
     config.ignoreShadowsDisplay=YES;config.ignoreShadowsSingleWindow=YES;
     config.captureResolution=SCCaptureResolutionBest;config.pixelFormat=kCVPixelFormatType_32BGRA;
     if([result isAbandoned]){[config release];[filter release];return;}
     [SCScreenshotManager captureImageWithFilter:filter configuration:config completionHandler:^(CGImageRef image,NSError *captureError){
      @autoreleasepool {
       if([result isAbandoned])return;
       if(captureError||!image){[result completePNG:nil error:captureError?:cuevia_screenshot_error(-3903,@"캡처 이미지가 비어 있어요")];return;}
       NSMutableData *data=[NSMutableData data];
       CGImageDestinationRef destination=CGImageDestinationCreateWithData((CFMutableDataRef)data,CFSTR("public.png"),1,NULL);
       if(!destination){[result completePNG:nil error:cuevia_screenshot_error(-3903,@"PNG 저장을 준비하지 못했어요")];return;}
       CGImageDestinationAddImage(destination,image,NULL);
       BOOL encoded=CGImageDestinationFinalize(destination);CFRelease(destination);
       if(!encoded||data.length==0||data.length>(32u<<20)){[result completePNG:nil error:cuevia_screenshot_error(-3903,@"캡처 이미지를 PNG로 저장하지 못했어요")];return;}
       [result completePNG:data error:nil];
      }
     }];
     [config release];[filter release];
    }
   }];
   int bounded_ms=timeout_ms>0&&timeout_ms<30000?timeout_ms:30000;
   if(dispatch_semaphore_wait(result->completion,dispatch_time(DISPATCH_TIME_NOW,(int64_t)bounded_ms*NSEC_PER_MSEC))!=0){
    [result abandon];[result release];*failure=strdup("화면 캡처 응답 시간이 초과됐어요. 다시 시도해주세요");return -3902;
   }
   [result->lock lock];
   int status=(int)result->code;
   if(status==0&&result->png.length>0){
    *output_size=result->png.length;*output=malloc(*output_size);
    if(*output)memcpy(*output,result->png.bytes,*output_size);
    else {status=-3903;*output_size=0;*failure=strdup("캡처 이미지 메모리를 할당하지 못했어요");}
   } else {
    status=status?:-3903;
    *failure=strdup((result->message?:@"화면을 캡처하지 못했어요").UTF8String);
   }
   [result->lock unlock];[result release];return status;
  }
 }
 *failure=strdup("화면 캡처는 macOS 14 이상에서 사용할 수 있어요");return -3900;
}
