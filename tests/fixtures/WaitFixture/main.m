#import <UIKit/UIKit.h>

@interface WaitFixtureDelegate : UIResponder <UIApplicationDelegate>
@property(nonatomic, strong) UIWindow *window;
@property(nonatomic, strong) UILabel *status;
@property(nonatomic, strong) UILabel *cover;
@property(nonatomic, strong) UILabel *timerLabel;
@property(nonatomic, strong) UILabel *startCountLabel;
@property(nonatomic, strong) NSTimer *timer;
@property(nonatomic) NSUInteger ticks;
@property(nonatomic) NSUInteger starts;
@end

@implementation WaitFixtureDelegate
- (UILabel *)label:(NSString *)text identifier:(NSString *)identifier frame:(CGRect)frame {
    UILabel *label = [[UILabel alloc] initWithFrame:frame];
    label.text = text;
    label.accessibilityIdentifier = identifier;
    label.isAccessibilityElement = YES;
    return label;
}
- (UIButton *)button:(NSString *)title action:(SEL)action frame:(CGRect)frame {
    UIButton *button = [UIButton buttonWithType:UIButtonTypeSystem];
    button.frame = frame;
    [button setTitle:title forState:UIControlStateNormal];
    [button addTarget:self action:action forControlEvents:UIControlEventTouchUpInside];
    return button;
}
- (BOOL)application:(UIApplication *)application didFinishLaunchingWithOptions:(NSDictionary *)options {
    self.window = [[UIWindow alloc] initWithFrame:UIScreen.mainScreen.bounds];
    UIViewController *controller = [UIViewController new];
    controller.view.backgroundColor = UIColor.systemBackgroundColor;
    UIButton *start = [self button:@"Start delayed result" action:@selector(startResult) frame:CGRectMake(20, 100, 340, 50)];
    self.status = [self label:@"Waiting for start" identifier:@"result-status" frame:CGRectMake(20, 165, 340, 45)];
    UILabel *covered = [self label:@"Covered evidence" identifier:@"covered-evidence" frame:CGRectMake(20, 240, 340, 60)];
    self.cover = [self label:@"Opaque cover" identifier:@"opaque-cover" frame:covered.frame];
    self.cover.backgroundColor = UIColor.systemGrayColor;
    UIButton *reveal = [self button:@"Reveal delayed evidence" action:@selector(revealEvidence) frame:CGRectMake(20, 320, 340, 50)];
    self.timerLabel = [self label:@"Tick 0" identifier:@"unrelated-timer" frame:CGRectMake(20, 400, 340, 40)];
    self.startCountLabel = [self label:@"Starts 0" identifier:@"start-count" frame:CGRectMake(20, 450, 340, 40)];
    NSArray *elements = @[start, self.status, covered, self.cover, reveal, self.timerLabel, self.startCountLabel];
    for (UIView *view in elements) [controller.view addSubview:view];
    controller.view.accessibilityElements = elements;
    self.window.rootViewController = controller;
    [self.window makeKeyAndVisible];
    self.timer = [NSTimer scheduledTimerWithTimeInterval:0.1 target:self selector:@selector(tick) userInfo:nil repeats:YES];
    return YES;
}
- (void)startResult {
    self.startCountLabel.text = [NSString stringWithFormat:@"Starts %lu", (unsigned long)++self.starts];
    self.status.text = @"Loading";
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{ self.status.text = @"Ready"; });
}
- (void)revealEvidence {
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, NSEC_PER_SEC), dispatch_get_main_queue(), ^{ self.cover.hidden = YES; });
}
- (void)tick {
    self.timerLabel.text = [NSString stringWithFormat:@"Tick %lu", (unsigned long)++self.ticks];
}
@end

int main(int argc, char **argv) {
    @autoreleasepool { return UIApplicationMain(argc, argv, nil, NSStringFromClass(WaitFixtureDelegate.class)); }
}
