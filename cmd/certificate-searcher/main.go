package main

import (
	"bufio"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pkg/profile"
	cs "github.com/teamnsrg/certificate-searcher"
	"github.com/teamnsrg/zcrypto/x509"
	"github.com/teamnsrg/zcrypto/x509/pkix"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"sync"
)

var log *zap.SugaredLogger

type LabeledCertChain struct {
	AbuseLabels     []string          `json:"abuse_labels"`
	Leaf            *x509.Certificate `json:"leaf,omitempty"`
	LeafParent      *x509.Certificate `json:"leaf_parent,omitempty"`
	Root            *x509.Certificate `json:"root,omitempty"`
	ChainDepth      int               `json:"chain_depth,omitempty"`
	ValidationLevel string            `json:"validation_level,omitempty"`
	LeafValidLength int               `json:"leaf_valid_len,omitempty"`
	MatchedDomains  string            `json:"matched_domains,omitempty"`
}

func initLogger() {
	atom := zap.NewAtomicLevelAt(zap.InfoLevel)
	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.Lock(os.Stdout),
		atom), zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer logger.Sync()
	log = logger.Sugar()
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

func verifyPathExists(path string) {
	if ok, err := pathExists(path); err != nil || !ok {
		log.Errorf("Invalid input file/directory: %s\n", path)
		if err != nil {
			log.Errorf("%s\n", err.Error())
		}
		os.Exit(1)
	}
}

func isDirectory(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fileInfo.IsDir(), nil
}

func getDirectoryFiles(dirPath string) ([]string, error) {
	filepaths := make([]string, 0)
	if files, err := ioutil.ReadDir(dirPath); err != nil {
		return filepaths, err
	} else {
		baseDir := strings.TrimSuffix(dirPath, "/")
		for _, info := range files {
			filepaths = append(filepaths, baseDir+"/"+info.Name())
		}
	}

	return filepaths, nil
}

func getFilesForPath(path string) (filepaths []string, err error) {
	if isDir, err := isDirectory(path); err == nil && isDir {
		filepaths, err = getDirectoryFiles(path)
	} else if !isDir {
		filepaths = []string{path}
	}

	return
}

func readCSVFiles(filepaths []string, dataRows chan []string, wg *sync.WaitGroup) {
	for _, filepath := range filepaths {
		log.Infof("reading file %s", filepath)
		f, err := os.Open(filepath)
		if err != nil {
			log.Error(err)
			continue
		}

		reader := csv.NewReader(f)

		var record []string
		for record, err = reader.Read(); err == nil; record, err = reader.Read() {
			dataRows <- record
		}
		if err != io.EOF {
			log.Error(err)
		}
		
		f.Close()
	}
	wg.Done()
}

func parseCertificateNamesOnly(bytes []byte) (*x509.Certificate, error) {
	cert := &x509.Certificate{}
	cert.Raw = make([]byte, len(bytes))
	copy(cert.Raw, bytes)
	cert.DNSNames = make([]string, 0)
	cert.Subject = pkix.Name{}
	offset := 0
	var err error
	for _, asn1Obj := range cs.CertObjs {
		switch asn1Obj.Name {
		case "Subject":
			var subjectName *pkix.Name
			subjectName, offset, err = asn1Obj.SubjectCommonName(bytes, offset)
			if subjectName != nil {
				cert.Subject = *subjectName
			}
		case "Extensions":
			var subjectAltNames []string
			subjectAltNames, offset, err = asn1Obj.SubjectAltName(bytes, offset)
			if subjectAltNames != nil {
				cert.DNSNames = append(cert.DNSNames, subjectAltNames...)
			}
		default:
			offset, err = asn1Obj.AdvanceOffset(bytes, offset)
		}

		if err != nil {
			switch err.(type) {
			case cs.MissingExtensionError:
				return cert, nil
			default:
				return cert, err
			}
		}

	}

	return cert, err
}

func decodeAndParseChain(encodedCertChain []string, parser *x509.CertParser, onlyParseName bool) ([]*x509.Certificate, error) {
	certChain := make([]*x509.Certificate, 0)
	for _, encodedCert := range encodedCertChain {
		certBytes, err := base64.StdEncoding.DecodeString(encodedCert)
		if err != nil {
			return nil, err
		}

		var cert *x509.Certificate
		if onlyParseName {
			cert, err = parseCertificateNamesOnly(certBytes)
		} else {
			cert, err = parser.ParseCertificate(certBytes)
		}

		if err != nil {
			log.Errorf("Unable to parse certificate %s due to %s", encodedCert, err)
			return nil, err
		}
		certChain = append(certChain, cert)
	}

	return certChain, nil
}

func extractFeaturesToJSON(chain []*x509.Certificate, labels []string) (*LabeledCertChain, error) {
	var leaf, leafParent *x509.Certificate
	leaf = chain[0]
	if len(chain) > 1 {
		leafParent = chain[1]
	}

	certChain := &LabeledCertChain{
		AbuseLabels: labels,
		Leaf:        leaf,
		LeafParent:  leafParent,
		Root:        chain[len(chain)-1],
		ChainDepth:  len(chain),
	}

	return certChain, nil
}

func prettyParseCertificate(encodedCertChain []string, parser *x509.CertParser, labels []string) string {
	certChain, err := decodeAndParseChain(encodedCertChain, parser, false)
	processedChain, err := extractFeaturesToJSON(certChain, labels)
	if err != nil {
		log.Fatal(err)
	}

	jsonBytes, err := json.Marshal(processedChain)
	if err != nil {
		log.Fatal(err)
	}

	return string(jsonBytes)
}

func processCertificates(dataRows chan []string, outputStrings chan string, labelers []cs.DomainLabeler, onlyParseNames bool, baseDomains map[string]struct{},wg *sync.WaitGroup) {
	const CERT_INDEX int = 1
	const CHAIN_INDEX int = 3
	const CHAIN_DELIMETER string = "|"

	parser := x509.NewCertParser()

	for row := range dataRows {
		certB64 := row[CERT_INDEX]
		chainB64 := strings.Split(strings.TrimSpace(row[CHAIN_INDEX]), CHAIN_DELIMETER)

		if chainB64[0] != certB64 {
			chainB64 = append([]string{certB64}, chainB64...)
		}

		certChain, err := decodeAndParseChain(chainB64, parser, onlyParseNames)
		if err != nil {
			log.Error(err)
			continue
		}

		leafCert := certChain[0]

		certLabelMap := make(map[cs.DomainLabel]struct{})
		for _, name := range leafCert.DNSNames {
			if _, present := baseDomains[name]; present {
				continue
			}

			for _, labeler := range labelers {
				labels := labeler.LabelDomain(name)
				if len(labels) > 0 {
					for _, label := range labels {
						certLabelMap[label] = struct{}{}
					}
				}
			}
		}

		if len(certLabelMap) > 0 {
			certLabels := make([]string, 0)
			for domainLabel, _ := range certLabelMap {
				certLabels = append(certLabels, domainLabel.String())
			}

			outputStrings <- prettyParseCertificate(chainB64, parser, certLabels)
		}
	}

	wg.Done()
}

func writeOutput(outputStrings chan string, outputFilename string, wg *sync.WaitGroup) {
	var outputFile *os.File
	var err error

	if outputFilename == "-" {
		outputFile = os.Stdout
	} else if len(outputFilename) > 0 {
		outputFile, err = os.Create(outputFilename)
		if err != nil {
			log.Fatal(err)
		}
	}

	w := bufio.NewWriterSize(outputFile, 4096*1000)

	for output := range outputStrings {
		w.WriteString(output + "\n")
	}
	w.Flush()

	outputFile.Close()
	wg.Done()
}

// Command line flags
var (
	outputFilepath = flag.String("o", "-", "Output file for certificate")
	workerCount    = flag.Int("workers", runtime.NumCPU(), "Number of parallel parsers/json unmarshallers")
	memProfile     = flag.Bool("mem-profile", false, "Run memory profiling")
	cpuProfile     = flag.Bool("cpu-profile", false, "Run cpu profiling")
	namesOnly      = flag.Bool("names-only", false, "only parse names from cert (faster)")
	domainFilepath = flag.String("domains", "", ".txt file with base domain names for name-similarity labeling")
	usage          = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s: %s <flags> <input-file-or-dir>\n", os.Args[0], os.Args[0])
		fmt.Print("Flags:\n")
		flag.PrintDefaults()
	}
)

func main() {
	initLogger()

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	var baseDomains []string
	defaultDomains := []string{
		"www.google.com",
		"www.youtube.com",
		"www.tmall.com",
		"www.facebook.com",
		"www.baidu.com",
		"www.apple.com",
	}

	if *domainFilepath == "" {
		log.Infof("No base domain file specified, using default list of %d domains", len(defaultDomains))
		baseDomains = defaultDomains
	} else {
		f, err := os.Open(*domainFilepath)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()

		baseDomains = make([]string, 0)
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			rawDomain := strings.TrimSpace(scanner.Text())
			sanitizedDomain := strings.ToLower(rawDomain)
			baseDomains = append(baseDomains, sanitizedDomain)

			if rawDomain != sanitizedDomain {
				log.Warnf("domain %s was sanitized to %s", rawDomain, sanitizedDomain)
			}
		}
	}
	baseDomainMap := make(map[string]struct{})
	for _, domain := range baseDomains {
		baseDomainMap[domain] = struct{}{}
	}


	if *cpuProfile {
		defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
	}
	if *memProfile {
		defer profile.Start(profile.MemProfile, profile.ProfilePath("."), profile.NoShutdownHook).Stop()
	}

	inputPath := flag.Arg(0)
	verifyPathExists(inputPath)

	filepaths, err := getFilesForPath(inputPath)
	if err != nil {
		log.Fatalf("Unable to get files for path %s", inputPath)
	}

	log.Info("building domain labelers")

	domainLabelers := []cs.DomainLabeler{
		cs.NewTypoSquattingLabeler(&baseDomains),
		cs.NewTargetEmbeddingLabeler(&baseDomains),
		//cs.NewHomoGraphLabeler(&baseDomains), //TODO: fix issues with aa2.csv
		cs.NewBitSquattingLabeler(&baseDomains),
		cs.NewPhishTankLabeler(),
		cs.NewSafeBrowsingLabeler(),
	}

	dataRows := make(chan []string, *workerCount)
	readWG := &sync.WaitGroup{}
	readWG.Add(1)
	go readCSVFiles(filepaths, dataRows, readWG)

	outputStrings := make(chan string)
	workerWG := &sync.WaitGroup{}
	for i := 0; i < *workerCount; i++ {
		workerWG.Add(1)
		go processCertificates(dataRows, outputStrings, domainLabelers, *namesOnly, baseDomainMap, workerWG)
	}

	writeWG := &sync.WaitGroup{}
	writeWG.Add(1)
	go writeOutput(outputStrings, *outputFilepath, writeWG)

	readWG.Wait()
	close(dataRows)
	workerWG.Wait()
	close(outputStrings)
	writeWG.Wait()
}
