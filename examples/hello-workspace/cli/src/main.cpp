#include <iostream>

#include "greeter/greeter.h"

int main() {
    std::cout << hello::greeting("workspace") << '\n';
    return 0;
}
