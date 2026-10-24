#include "greeter/greeter.h"

int main() {
    return hello::greeting("workspace") == "Hello, workspace!" ? 0 : 1;
}
