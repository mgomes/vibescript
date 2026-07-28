- **Fixed: detecting a crossed `Time` format rendered it first.** Classifying
  `t.format("%1000000000N")` ran the strftime renderer, which honors a
  directive's requested width, allocating about a gigabyte purely to decide --
  and with no memory limit set that exhausts the process. Detection now scans
  for a recognized directive instead of rendering.
