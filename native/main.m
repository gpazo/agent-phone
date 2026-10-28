#import <Foundation/Foundation.h>
#import <CoreFoundation/CoreFoundation.h>

NSDictionary *JevSimulatorAction(NSDictionary *request);

// One persistent guest process per phone. Only bounded accessibility actions
// are exposed; app storage and arbitrary shell execution are not part of this API.
int main(void) {
    dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
        char *line = NULL;
        size_t capacity = 0;
        while (getline(&line, &capacity, stdin) > 0) {
            @autoreleasepool {
                NSError *error = nil;
                NSData *data = [[NSString stringWithUTF8String:line] dataUsingEncoding:NSUTF8StringEncoding];
                id request = [NSJSONSerialization JSONObjectWithData:data options:0 error:&error];
                NSDictionary *result = [request isKindOfClass:NSDictionary.class]
                    ? JevSimulatorAction(request) : @{@"ok": @NO, @"error": @"Expected an action object"};
                NSData *reply = [NSJSONSerialization dataWithJSONObject:result options:0 error:&error];
                if (!reply) break;
                fwrite(reply.bytes, 1, reply.length, stdout);
                fputc('\n', stdout);
                fflush(stdout);
            }
        }
        free(line);
        CFRunLoopStop(CFRunLoopGetMain());
    });
    CFRunLoopRun();
    return 0;
}
