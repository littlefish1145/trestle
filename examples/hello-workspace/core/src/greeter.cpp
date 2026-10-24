#include "greeter/greeter.h"

namespace hello {

std::string greeting(const std::string& name) {
    return "Hello, " + name + "!";
}

}  // namespace hello
