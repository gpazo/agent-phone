/*
 * The runtime selectors and surface selection follow the interfaces documented by
 * facebook/idb at 1387b360a9dcad34573cb1d9f277e5a0eacfd804. See THIRD_PARTY.md.
 */
#import <CoreGraphics/CoreGraphics.h>
#import <CoreImage/CoreImage.h>
#import <Foundation/Foundation.h>
#import <ImageIO/ImageIO.h>
#import <IOSurface/IOSurface.h>
#import <objc/message.h>
#import <objc/runtime.h>
#import <dlfcn.h>
#import <arpa/inet.h>
#import <errno.h>
#import <fcntl.h>
#import <poll.h>
#import <signal.h>
#import <stdint.h>
#import <stdio.h>
#import <stdlib.h>
#import <string.h>
#import <time.h>
#import <unistd.h>

static volatile sig_atomic_t gStopRequested = 0;
static volatile sig_atomic_t gSignalPipeWriteFD = -1;
static const uint32_t kOutputWidth = 402;
static const uint64_t kFrameIntervalNS = 250000000ULL;
static pid_t gParentPID;
static const size_t kMaxJPEGBytes = 2 * 1024 * 1024;
static const size_t kMaxSurfaceSide = 8192;

static void Log(const char *format, ...) {
  va_list args;
  va_start(args, format);
  vfprintf(stderr, format, args);
  fputc('\n', stderr);
  va_end(args);
}

static void LogSelectorIssueOnce(const char *selector, const char *detail) {
  static const char *reported[32];
  static size_t count = 0;
  for (size_t i = 0; i < count; i++) {
    if (strcmp(reported[i], selector) == 0) return;
  }
  if (count < sizeof(reported) / sizeof(reported[0])) reported[count++] = selector;
  Log("selector %s: %s", selector, detail);
}

static id SendObject(id target, const char *selectorName) {
  if (!target) return nil;
  @try { return ((id (*)(id, SEL))objc_msgSend)(target, sel_registerName(selectorName)); }
  @catch (NSException *exception) {
    LogSelectorIssueOnce(selectorName, exception.name.UTF8String);
    return nil;
  }
}

static BOOL SendUnsignedShort(id target, const char *selectorName, unsigned short *value) {
  if (!target) return NO;
  @try {
    *value = ((unsigned short (*)(id, SEL))objc_msgSend)(target, sel_registerName(selectorName));
    return YES;
  } @catch (NSException *exception) {
    LogSelectorIssueOnce(selectorName, exception.name.UTF8String);
    return NO;
  }
}

static BOOL IsBooted(id device) {
  @try { return ((NSUInteger (*)(id, SEL))objc_msgSend)(device, sel_registerName("state")) == 3; }
  @catch (NSException *exception) { Log("device state failed: %s", exception.name.UTF8String); return NO; }
}

static NSString *CanonicalUDID(id value) {
  if ([value isKindOfClass:[NSUUID class]]) return ((NSUUID *)value).UUIDString.lowercaseString;
  if ([value isKindOfClass:[NSString class]]) {
    NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:value];
    return uuid.UUIDString.lowercaseString;
  }
  return nil;
}

static NSString *FrameworkPath(NSString *name, NSString *developerDir) {
  NSArray<NSString *> *roots = @[
    @"/Library/Developer/PrivateFrameworks",
    [developerDir stringByAppendingPathComponent:@"Library/PrivateFrameworks"]
  ];
  for (NSString *root in roots) {
    NSString *path = [[root stringByAppendingPathComponent:name] stringByStandardizingPath];
    BOOL isDirectory = NO;
    if ([[NSFileManager defaultManager] fileExistsAtPath:path isDirectory:&isDirectory] && isDirectory) return path;
  }
  return nil;
}

static BOOL LoadFramework(NSString *frameworkPath, NSString *binaryName) {
  if (!frameworkPath) return NO;
  NSString *binary = [frameworkPath stringByAppendingPathComponent:binaryName];
  void *handle = dlopen(binary.fileSystemRepresentation, RTLD_NOW | RTLD_GLOBAL);
  if (!handle) {
    Log("dlopen %s failed: %s", binary.fileSystemRepresentation, dlerror() ?: "unknown error");
    return NO;
  }
  return YES;
}

static id DeviceForUDID(id deviceSet, NSString *udid) {
  id devices = SendObject(deviceSet, "devicesByUDID");
  if (![devices isKindOfClass:[NSDictionary class]]) return nil;
  NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:udid];
  id match = uuid ? devices[uuid] : nil;
  if (match) return match;
  match = devices[udid];
  if (match) return match;
  for (id key in devices) {
    if ([CanonicalUDID(key) isEqualToString:udid]) return devices[key];
  }
  return nil;
}

static id MainDisplayDescriptor(id ioClient) {
  id ports = SendObject(ioClient, "ioPorts");
  if (![ports isKindOfClass:[NSArray class]]) {
    Log("CoreSimulator returned no IO-port array");
    return nil;
  }
  id primary = nil;
  for (id port in ports) {
    id descriptor = SendObject(port, "descriptor");
    id state = SendObject(descriptor, "state");
    unsigned short displayClass = UINT16_MAX;
    if (state && SendUnsignedShort(state, "displayClass", &displayClass) && displayClass == 0) {
      if (primary) { Log("multiple class-0 displays are ambiguous"); return nil; }
      primary = descriptor;
    }
  }
  if (!primary) Log("no primary class-0 display");
  return primary;
}

static uint64_t MonotonicNanoseconds(void) {
  struct timespec value;
  clock_gettime(CLOCK_MONOTONIC, &value);
  return (uint64_t)value.tv_sec * 1000000000ULL + (uint64_t)value.tv_nsec;
}

static BOOL ParentAlive(void) {
  return !gStopRequested && getppid() == gParentPID;
}

static BOOL WriteAll(int fd, const void *bytes, size_t length) {
  const uint8_t *cursor = bytes;
  while (length > 0 && ParentAlive()) {
    ssize_t written = write(fd, cursor, length);
    if (written < 0 && errno == EINTR) continue;
    if (written < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
      struct pollfd fds[2] = {{.fd = fd, .events = POLLOUT}, {.fd = STDIN_FILENO, .events = POLLIN}};
      int result = poll(fds, 2, 100);
      if (result < 0 && errno != EINTR) return NO;
      if (fds[1].revents & (POLLHUP | POLLERR | POLLNVAL)) return NO;
      if (fds[1].revents & POLLIN) { uint8_t byte; if (read(STDIN_FILENO, &byte, 1) <= 0) return NO; }
      if (fds[0].revents & (POLLHUP | POLLERR | POLLNVAL)) return NO;
      continue;
    }
    if (written <= 0) return NO;
    cursor += (size_t)written;
    length -= (size_t)written;
  }
  return length == 0;
}

static BOOL EmitFrame(CIContext *context, CGColorSpaceRef colorSpace, IOSurfaceRef surface,
                     uint64_t sequence, NSString *udid) {
  size_t sourceWidth = IOSurfaceGetWidth(surface);
  size_t sourceHeight = IOSurfaceGetHeight(surface);
  if (sourceWidth == 0 || sourceHeight == 0 || sourceWidth > kMaxSurfaceSide || sourceHeight > kMaxSurfaceSide) {
    Log("surface %u has unsupported dimensions %zux%zu", IOSurfaceGetID(surface), sourceWidth, sourceHeight);
    return NO;
  }

  @autoreleasepool {
    NSDate *capturedAt = [NSDate date];
    NSDictionary *options = @{kCIImageColorSpace: (__bridge id)colorSpace};
    CIImage *source = [CIImage imageWithIOSurface:surface options:options];
    if (!source) {
      Log("CIImage could not wrap IOSurface %u", IOSurfaceGetID(surface));
      return NO;
    }
    CGRect extent = source.extent;
    source = [source imageByApplyingTransform:CGAffineTransformMakeTranslation(-CGRectGetMinX(extent), -CGRectGetMinY(extent))];

    size_t outputHeight = (size_t)llround((double)sourceHeight * (double)kOutputWidth / (double)sourceWidth);
    if (outputHeight == 0 || outputHeight > kMaxSurfaceSide) return NO;
    CGFloat yScale = (CGFloat)outputHeight / (CGFloat)sourceHeight;
    CGFloat xScale = (CGFloat)kOutputWidth / (CGFloat)sourceWidth;
    CIFilter *filter = [CIFilter filterWithName:@"CILanczosScaleTransform"];
    if (!filter) {
      Log("CILanczosScaleTransform is unavailable");
      return NO;
    }
    [filter setValue:source forKey:kCIInputImageKey];
    [filter setValue:@(yScale) forKey:@"inputScale"];
    [filter setValue:@(xScale / yScale) forKey:@"inputAspectRatio"];
    CIImage *scaled = filter.outputImage;
    CGRect outputRect = CGRectMake(0, 0, kOutputWidth, outputHeight);
    CGImageRef image = [context createCGImage:scaled fromRect:outputRect];
    if (!image) {
      Log("CIContext failed to render surface %u", IOSurfaceGetID(surface));
      return NO;
    }

    NSMutableData *jpeg = [NSMutableData dataWithCapacity:128 * 1024];
    CGImageDestinationRef destination = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)jpeg, CFSTR("public.jpeg"), 1, NULL);
    if (!destination) {
      CGImageRelease(image);
      Log("ImageIO could not create a JPEG destination");
      return NO;
    }
    NSDictionary *properties = @{(__bridge NSString *)kCGImageDestinationLossyCompressionQuality: @0.68};
    CGImageDestinationAddImage(destination, image, (__bridge CFDictionaryRef)properties);
    BOOL finalized = CGImageDestinationFinalize(destination);
    CFRelease(destination);
    CGImageRelease(image);
    if (!finalized || jpeg.length == 0 || jpeg.length > kMaxJPEGBytes) {
      Log("ImageIO returned an invalid JPEG length (%lu)", (unsigned long)jpeg.length);
      return NO;
    }

    static NSISO8601DateFormatter *formatter;
    if (!formatter) {
      formatter = [NSISO8601DateFormatter new];
      formatter.formatOptions = NSISO8601DateFormatWithInternetDateTime | NSISO8601DateFormatWithFractionalSeconds;
    }
    NSDictionary *record = @{ @"type": @"frame", @"device": udid, @"sequence": @(sequence),
      @"capturedAt": [formatter stringFromDate:capturedAt], @"width": @(kOutputWidth),
      @"height": @(outputHeight), @"jpegBytes": @(jpeg.length) };
    NSData *metadata = [NSJSONSerialization dataWithJSONObject:record options:0 error:nil];
    if (!metadata || metadata.length == 0 || metadata.length > 8192) return NO;
    uint32_t lengthBE = htonl((uint32_t)metadata.length);
    if (!WriteAll(STDOUT_FILENO, &lengthBE, sizeof(lengthBE)) ||
        !WriteAll(STDOUT_FILENO, metadata.bytes, metadata.length)) return NO;
    lengthBE = htonl((uint32_t)jpeg.length);
    if (!WriteAll(STDOUT_FILENO, &lengthBE, sizeof(lengthBE)) ||
        !WriteAll(STDOUT_FILENO, jpeg.bytes, jpeg.length)) return NO;
    return YES;
  }
}

static void HandleSignal(int signalNumber) {
  (void)signalNumber;
  gStopRequested = 1;
  int fd = gSignalPipeWriteFD;
  if (fd >= 0) {
    const uint8_t byte = 1;
    (void)write(fd, &byte, sizeof(byte));
  }
}

static void DrainFD(int fd) {
  uint8_t bytes[128];
  while (read(fd, bytes, sizeof(bytes)) > 0) {}
}

static int RunVision(NSString *udid) {
  NSString *developerDir = NSProcessInfo.processInfo.environment[@"DEVELOPER_DIR"];
  if (developerDir.length == 0) {
    NSTask *select = [NSTask new];
    select.executableURL = [NSURL fileURLWithPath:@"/usr/bin/xcode-select"];
    select.arguments = @[@"-p"];
    NSPipe *output = [NSPipe pipe];
    select.standardOutput = output;
    select.standardError = [NSFileHandle fileHandleWithNullDevice];
    NSError *launchError = nil;
    if (![select launchAndReturnError:&launchError]) { Log("xcode-select failed"); return 2; }
    NSData *data = [output.fileHandleForReading readDataToEndOfFile];
    [select waitUntilExit];
    developerDir = [[[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding] stringByTrimmingCharactersInSet:NSCharacterSet.whitespaceAndNewlineCharacterSet];
    if (select.terminationStatus != 0 || developerDir.length == 0) { Log("select a full Xcode developer directory"); return 2; }
  }
  NSString *coreSimulatorPath = FrameworkPath(@"CoreSimulator.framework", developerDir);
  if (!LoadFramework(coreSimulatorPath, @"CoreSimulator")) {
    Log("CoreSimulator.framework was not found under the selected Xcode or /Library/Developer");
    return 2;
  }
  NSString *deviceIOPath = [coreSimulatorPath stringByAppendingPathComponent:@"Frameworks/CoreSimDeviceIO.framework"];
  if ([[NSFileManager defaultManager] fileExistsAtPath:deviceIOPath] && !LoadFramework(deviceIOPath, @"CoreSimDeviceIO")) return 2;

  Class serviceClass = NSClassFromString(@"SimServiceContext");
  NSError *error = nil;
  id service = nil;
  SEL sharedSelector = sel_registerName("sharedServiceContextForDeveloperDir:error:");
  if (serviceClass && [serviceClass respondsToSelector:sharedSelector]) {
    id (*send)(id, SEL, id, NSError **) = (void *)objc_msgSend;
    @try { service = send(serviceClass, sharedSelector, developerDir, &error); }
    @catch (NSException *exception) { Log("selector sharedServiceContextForDeveloperDir:error: raised %s", exception.name.UTF8String); }
  } else {
    LogSelectorIssueOnce("sharedServiceContextForDeveloperDir:error:", "selector unavailable on SimServiceContext");
  }
  if (!service) {
    Log("CoreSimulator connection failed: %s", error.localizedDescription.UTF8String ?: "no service context");
    return 2;
  }

  SEL setSelector = sel_registerName("defaultDeviceSetWithError:");
  id deviceSet = nil;
  if ([service respondsToSelector:setSelector]) {
    id (*send)(id, SEL, NSError **) = (void *)objc_msgSend;
    @try { deviceSet = send(service, setSelector, &error); }
    @catch (NSException *exception) { Log("selector defaultDeviceSetWithError: raised %s", exception.name.UTF8String); }
  } else {
    LogSelectorIssueOnce("defaultDeviceSetWithError:", "selector unavailable on SimServiceContext");
  }
  if (!deviceSet) {
    Log("default device set lookup failed: %s", error.localizedDescription.UTF8String ?: "no device set");
    return 2;
  }

  id device = DeviceForUDID(deviceSet, udid);
  if (!device) {
    Log("UDID %s is not present in the CoreSimulator default device set", udid.UTF8String);
    return 2;
  }
  id actualUDID = SendObject(device, "UDID");
  if (![CanonicalUDID(actualUDID) isEqualToString:udid]) {
    Log("device UDID verification failed for requested %s", udid.UTF8String);
    return 2;
  }
  if (!IsBooted(device)) { Log("requested device is not Booted"); return 2; }
  id ioClient = SendObject(device, "io");
  if (!ioClient) {
    Log("device %s has no CoreSimulator IO client", udid.UTF8String);
    return 2;
  }
  id descriptor = MainDisplayDescriptor(ioClient);
  if (!descriptor) return 2;

  int signalPipe[2];
  if (pipe(signalPipe) != 0) {
    Log("pipe failed: %s", strerror(errno));
    return 2;
  }
  for (int i = 0; i < 2; i++) {
    int flags = fcntl(signalPipe[i], F_GETFL, 0);
    (void)fcntl(signalPipe[i], F_SETFL, flags | O_NONBLOCK);
    (void)fcntl(signalPipe[i], F_SETFD, FD_CLOEXEC);
  }
  gSignalPipeWriteFD = signalPipe[1];
  struct sigaction action;
  memset(&action, 0, sizeof(action));
  action.sa_handler = HandleSignal;
  sigemptyset(&action.sa_mask);
  sigaction(SIGINT, &action, NULL);
  sigaction(SIGTERM, &action, NULL);
  signal(SIGPIPE, SIG_IGN);

  CIContext *context = [CIContext contextWithOptions:@{kCIContextCacheIntermediates: @NO}];
  CGColorSpaceRef colorSpace = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
  if (!context || !colorSpace) {
    Log("Core Image or sRGB setup failed");
    close(signalPipe[0]); close(signalPipe[1]);
    gSignalPipeWriteFD = -1;
    return 2;
  }

  Log("connected Xcode=%s UDID=%s source=IOSurface sample_interval_ms=250 output_width=%u",
      developerDir.UTF8String, udid.UTF8String, kOutputWidth);
  Log("sampling selectors framebufferSurface then ioSurface; stdout uses 32-bit big-endian length frames");

  uint64_t sequence = 0;
  int status = 0;
  while (ParentAlive()) {
    uint64_t deadline = MonotonicNanoseconds() + kFrameIntervalNS;
    while (ParentAlive()) {
      uint64_t now = MonotonicNanoseconds();
      if (now >= deadline) break;
      uint64_t remaining = deadline - now;
      int timeoutMS = (int)((remaining + 999999ULL) / 1000000ULL);
      struct pollfd pollFDs[2] = {{.fd = signalPipe[0], .events = POLLIN}, {.fd = STDIN_FILENO, .events = POLLIN}};
      int result = poll(pollFDs, 2, timeoutMS);
      if (result > 0) {
        if (pollFDs[0].revents) DrainFD(signalPipe[0]);
        if (pollFDs[1].revents & (POLLHUP | POLLERR | POLLNVAL)) gStopRequested = 1;
        if (pollFDs[1].revents & POLLIN) { uint8_t byte; if (read(STDIN_FILENO, &byte, 1) <= 0) gStopRequested = 1; }
      }
      else if (result < 0 && errno != EINTR) {
        Log("poll failed: %s", strerror(errno));
        status = 2;
        gStopRequested = 1;
      }
    }
    if (!ParentAlive()) break;

    @autoreleasepool {
      if (!IsBooted(device)) { Log("device left Booted state"); status = 2; break; }
      id surfaceObject = SendObject(descriptor, "framebufferSurface");
      if (!surfaceObject) surfaceObject = SendObject(descriptor, "ioSurface");
      Class surfaceClass = NSClassFromString(@"IOSurface");
      if (surfaceObject && surfaceClass && [surfaceObject isKindOfClass:surfaceClass]) {
        IOSurfaceRef surface = (__bridge IOSurfaceRef)surfaceObject;
        IOSurfaceIncrementUseCount(surface);
        BOOL emitted = EmitFrame(context, colorSpace, surface, ++sequence, udid);
        IOSurfaceDecrementUseCount(surface);
        if (!emitted) {
          if (errno == EPIPE) Log("frame consumer closed stdout");
          status = 1;
          gStopRequested = 1;
        }
      } else {
        Log("primary display lost its IOSurface");
        status = 2;
        break;
      }
    }
  }

  CGColorSpaceRelease(colorSpace);
  gSignalPipeWriteFD = -1;
  close(signalPipe[0]);
  close(signalPipe[1]);
  Log("stopped after %llu emitted frame(s)", (unsigned long long)sequence);
  return status;
}

int main(int argc, const char *argv[]) {
  @autoreleasepool {
    if (argc != 2) {
      fprintf(stderr, "usage: %s <simulator-udid>\n", argv[0]);
      return 64;
    }
    NSString *input = [NSString stringWithUTF8String:argv[1]];
    NSUUID *uuid = [[NSUUID alloc] initWithUUIDString:input];
    if (!uuid) {
      fprintf(stderr, "simulator UDID must be a UUID\n");
      return 64;
    }
    gParentPID = getppid();
    int flags = fcntl(STDOUT_FILENO, F_GETFL, 0);
    if (flags < 0 || fcntl(STDOUT_FILENO, F_SETFL, flags | O_NONBLOCK) < 0) return 2;
    return RunVision(uuid.UUIDString.lowercaseString);
  }
}
