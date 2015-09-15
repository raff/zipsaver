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
	fileHeaderSignature      = 0x04034b50
	directoryHeaderSignature = 0x02014b50
	dataDescriptorSignature  = 0x08074b50 // de-facto standard; required by OS X Finder
	fileHeaderLen            = 30         // + filename + extra
	dataDescriptorLen        = 16         // four uint32: descriptor signature, crc32, compressed size, size
	dataDescriptor64Len      = 24         // descriptor with 8 byte sizes

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

	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatal("usage: ", path.Base(os.Args[0]), " {zip-file}")
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
			fmt.Println(hex.Dump(fh[:]))
		}

		b := readBuf(fh[:])
		magic := b.uint32()
		version := b.uint16()
		flags := b.uint16()
		comp := b.uint16()
		ctime := b.uint16()
		cdate := b.uint16()
		crc32 := b.uint32()
		clen := b.uint32()
		ulen := b.uint32()
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

		if *debug {
			fmt.Println()
			fmt.Printf("magic   %08x\n", magic)
			fmt.Printf("version %04x\n", version)
			fmt.Printf("flags   %04x\n", flags)
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
				log.Fatal("missing lenght")
			}

		default:
			log.Fatal("unsupported compression mode ", comp)
		}

		if (flags & 0x08) != 0 {
			// data descriptor
			var dd [dataDescriptorLen]byte

			if _, err := io.ReadFull(r, dd[:]); err != nil {
				log.Fatal("data descriptor", err)
			}

			b := readBuf(dd[:])
			magic := b.uint32()
			crc32 = b.uint32()
			clen = b.uint32()
			ulen = b.uint32()

			if magic != dataDescriptorSignature {
				log.Fatal("invalid data descriptor signature ", magic)
			}

			if *debug {
				fmt.Println()
				fmt.Printf("magic   %08x\n", magic)
				fmt.Printf("crc32   %08x\n", crc32)
				fmt.Printf("compressed size   %d\n", clen)
				fmt.Printf("uncompressed size %d\n", ulen)
			}
		}

		if *view {
			pc := 100 - (clen * 100 / ulen)
			fmt.Printf("%8d  %6s  %8d  %2d%%  %08x  %s\n", ulen, ctype, clen, pc, crc32, filename)
		}
	}
}
