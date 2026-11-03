# Third-Party Notices

## Magpie

Selected provider protocol, image handling, and network-safety code is copied or adapted from Magpie:

- Upstream: https://github.com/yetone/magpie
- Baseline: `4534561f6bb8f8353a67801fbe864a6502517022`
- License: MIT
- Copyright: Copyright (c) 2026 yetone

Copied source files identify their upstream paths. Modifications replace Magpie's gateway/provider registry with Satchel's narrow configuration and HTTP boundaries.

The following license must accompany copied or substantially adapted portions:

```text
MIT License

Copyright (c) 2026 yetone

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

Go module dependencies retain their own licenses. Release packaging must include the notices required by the final dependency set; this file currently records source reuse, not a completed release bill of materials.
