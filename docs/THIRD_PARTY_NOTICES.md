# Third-party notices

DOpad itself is released under the [0BSD license](LICENSE). The components
below are **not** covered by that license; they keep their own terms.

## ViGEmClient (Windows builds only)

`ViGEmClient.dll` is the user-mode client library of the ViGEm framework. It is
loaded by the Windows build of DOpad, and it is embedded inside `DOpad.exe`
when the executable is built with the `vigemdll` tag (see the README).

- Project: https://github.com/nefarius/ViGEmClient
- License: MIT
- The MIT license requires that its copyright and permission notice be
  included with copies of the software. If you redistribute `DOpad.exe` (or
  `ViGEmClient.dll` on its own), keep this file with it.

```
MIT License

Copyright (c) 2018 Benjamin Höglinger-Stelzer

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## ViGEmBus (not bundled)

The ViGEmBus kernel driver is **not** included in DOpad and is not
redistributed by it. Users install it separately from its own releases page.
It is published under the BSD-3-Clause license, and the project has been
retired by its author.

- Project: https://github.com/nefarius/ViGEmBus
