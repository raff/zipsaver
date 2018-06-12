package main

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
)

// from archive/zip struct.go

const (
	fileHeaderSignature       = 0x04034b50
	directoryHeaderSignature  = 0x02014b50
	dataDescriptorSignature   = 0x08074b50 // de-facto standard; required by OS X Finder
	archiveExtraDataSignature = 0x08064b50
	fileHeaderLen             = 30 // + filename + extra
	dataDescriptorLen         = 12 // three uint32: crc32, compressed size, size (dataDescriptionSignature may not be there)
	dataDescriptor64Len       = 20 // descriptor with 8 byte sizes

	// version numbers
	zipVersion20 = 20 // 2.0
	zipVersion45 = 45 // 4.5 (reads and writes zip64 archives)
)

type readBuf []byte

func (b *readBuf) uint16() uint16 {
	v := binary.LittleEndian.Uint16(*b)
	*b = (*b)[2:]
	return v
}

func (b *readBuf) uint32() uint32 {
	v := binary.LittleEndian.Uint32(*b)
	*b = (*b)[4:]
	return v
}

func (b *readBuf) uint64() uint64 {
	v := binary.LittleEndian.Uint64(*b)
	*b = (*b)[8:]
	return v
}

func main() {
	debug := flag.Bool("debug", false, "print debug info")
	view := flag.Bool("v", false, "view list")
	out := flag.String("out", "", "write recovered files to output zip file")
	override := flag.Bool("override", false, "override existing files")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [options] {zip-file}\n", path.Base(os.Args[0]))
		fmt.Fprintf(os.Stderr, "options:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		return
	}

	zipfile := flag.Arg(0)

	f, err := os.Open(zipfile)
	if err != nil {
		log.Fatal("open ", err)
	}

	defer f.Close()

	r := bufio.NewReader(f)

	var outz *zip.Writer

	create_flags := os.O_RDWR | os.O_CREATE | os.O_TRUNC
	if !*override {
		create_flags |= os.O_EXCL
	}

	if len(*out) > 0 {
		outf, err := os.OpenFile(*out, create_flags, 0666)
		if err != nil {
			log.Fatal("create output", err)
		}

		outz = zip.NewWriter(outf)

		defer func() {
			outz.Close()
			outf.Close()
		}()
	}

Loop:
	for {
		var fh [fileHeaderLen]byte

		if _, err := io.ReadFull(r, fh[:]); err != nil {
			log.Println("file header", err)
			break Loop
		}

		if *debug {
			fmt.Println()
			fmt.Print(hex.Dump(fh[:]))
		}

		var clen, ulen uint64

		b := readBuf(fh[:])
		magic := b.uint32()
		version := b.uint16()
		flags := b.uint16()
		comp := b.uint16()
		ctime := b.uint16()
		cdate := b.uint16()
		crc32 := b.uint32()
		clen = uint64(b.uint32())
		ulen = uint64(b.uint32())
		flen := b.uint16()
		elen := b.uint16()

		ctype := ""

		if magic == directoryHeaderSignature {
			// got central directory. Done
			log.Println("found central directory")
			break Loop
		}

		if magic != fileHeaderSignature {
			log.Println("invalid file header signature ", fmt.Sprintf("%08x", magic))
			break Loop
		}

		sflags := ""

		if (flags & 0x01) != 0 {
			sflags += " Encrypted"
		}
		if comp == 6 {
			if (flags & 0x02) != 0 {
				sflags += " 8k"
			}
			if (flags & 0x04) != 0 {
				sflags += " 3SF"
			}
		} else if comp == 14 {
			if (flags & 0x02) != 0 {
				sflags += " EOS"
			}
		} else {
			switch (flags & 0x6) >> 1 {
			case 0:
				sflags += " Normal"
			case 1:
				sflags += " Max"
			case 2:
				sflags += " Fast"
			case 3:
				sflags += " SuperFast"
			}
		}
		if (flags & 0x08) != 0 {
			sflags += " DataDesc"
		}
		if (flags & 0x10) != 0 {
			sflags += " Patched"
		}
		if (flags & 0x20) != 0 {
			sflags += " StrongEncryption"
		}

		if *debug {
			fmt.Println()
			fmt.Printf("magic   %08x\n", magic)
			fmt.Printf("version %d\n", version)
			fmt.Printf("flags   %04x%s\n", flags, sflags)
			fmt.Printf("comp    %04x\n", comp)
			fmt.Printf("time    %04x\n", ctime)
			fmt.Printf("date    %04x\n", cdate)
			fmt.Printf("crc32   %08x\n", crc32)
			fmt.Printf("compressed size   %d\n", clen)
			fmt.Printf("uncompressed size %d\n", ulen)
			fmt.Printf("filename length   %d\n", flen)
			fmt.Printf("extra length      %d\n", elen)
		}

		fn := make([]byte, flen)
		if _, err := io.ReadFull(r, fn); err != nil {
			log.Println("read file name", err)
			break Loop
		}

		if *debug {
			fmt.Println()
			fmt.Println("filename", string(fn))
		}

		if elen > 0 {
			if _, err := io.CopyN(ioutil.Discard, r, int64(elen)); err != nil {
				log.Println("read extra", err)
				break Loop
			}
		}

		filename := string(fn)

		switch comp {
		case zip.Deflate:
			ctype = "Defl:N"

			var w io.Writer

			if *view {
				w = ioutil.Discard
			} else if outz != nil {
				fmt.Println("adding:", filename)
				if f, err := outz.Create(filename); err != nil {
					log.Fatal("create zip entry ", filename, err)
				} else {
					w = f
				}
			} else {
				fmt.Println("inflating:", filename)

				dir := filepath.Dir(filename)
				if dir != "" {
					if err := os.MkdirAll(dir, 0755); err != nil {
						log.Println("mkdir", dir, err)
					}
				}

				if f, err := os.OpenFile(filename, create_flags, 0666); err != nil {
					log.Fatal("create ", filename, err)
				} else {
					w = f
				}
			}

			dec := flate.NewReader(r)
			n, err := io.Copy(w, dec)
			if *debug {
				fmt.Println("decoded", n, "bytes")
			}
			if err != nil {
				if wc, ok := w.(io.Closer); ok {
					wc.Close()
					os.Remove(filename)
				}

				log.Println("decode file", err)
				break Loop
			} else {
				dec.Close()

				if wc, ok := w.(io.Closer); ok {
					wc.Close()
				}
			}

		case zip.Store:
			ctype = "Stored"

			if ulen > 0 {
				n, err := io.CopyN(ioutil.Discard, r, int64(ulen))
				if *debug {
					fmt.Println("read", n, "bytes")
				}
				if err != nil {
					log.Fatal("read file ", err)
				}
			} else {
				log.Fatal("missing length")
			}

		default:
			log.Fatal("unsupported compression mode ", comp)
		}

		if (flags & 0x08) != 0 {
			// data descriptor
			var dd [dataDescriptor64Len]byte

			dl := dataDescriptorLen
			if version >= zipVersion45 {
				dl = dataDescriptor64Len
			}

			if _, err := io.ReadFull(r, dd[0:4]); err != nil {
				log.Fatal("data descriptor header", err)
			}

			var hasMagic bool

			b := readBuf(dd[0:4])
			if b.uint32() == dataDescriptorSignature {
				hasMagic = true

				if _, err := io.ReadFull(r, dd[:dl]); err != nil {
					log.Fatal("data descriptor", err)
				}
			} else if _, err := io.ReadFull(r, dd[4:dl-4]); err != nil {
				log.Fatal("data descriptor", err)
			}

			b = readBuf(dd[0:dl])

			if version < zipVersion45 {
				crc32 = b.uint32()
				clen = uint64(b.uint32())
				ulen = uint64(b.uint32())
			} else {
				crc32 = b.uint32()
				clen = b.uint64()
				ulen = b.uint64()
			}

			if *debug {
				fmt.Println()
				if hasMagic {
					fmt.Printf("magic   %08x\n", dataDescriptorSignature)
				}
				fmt.Printf("crc32   %08x\n", crc32)
				fmt.Printf("compressed size   %d\n", clen)
				fmt.Printf("uncompressed size %d\n", ulen)
			}
		}

		if *view {
			pc := 0
			if ulen != 0 {
				pc = 100 - int(clen*100/ulen)
			}
			fmt.Printf("%8d  %6s  %8d  %2d%%  %08x  %s\n", ulen, ctype, clen, pc, crc32, filename)
		}
	}
}
